package kea

import (
	"context"
	"fmt"
	"time"

	"github.com/go-pg/pg/v9"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"isc.org/stork/server/agentcomm"
	dbmodel "isc.org/stork/server/database/model"
	storkutil "isc.org/stork/util"
)

// Periodic Puller that generates RPS interval data.
type RpsPuller struct {
	*agentcomm.PeriodicPuller
	PreviousRps map[int64]StatSample // map of last known values per Daemon
	Interval1   time.Duration
	Interval2   time.Duration
}

// Represents a time/value pair
type StatSample struct {
	SampledAt time.Time // time value was recorded
	Value     int64     // statistic value
}

// Represents a response from the single Kea server to the statistic-get
// for pkt4-ack-sent:
//{
//    "command": "statistic-get",
//    "arguments": {
//        "pkt4-ack-sent": [
//          [ 125, "2019-07-30 10:11:19.498739" ],
//            ...
//          ]
//    },
//    "result": 0
//}
type StatGetResponse4 struct {
	agentcomm.KeaResponseHeader
	Arguments *ResponseArguments4 `json:"arguments,omitempty"`
}

// The list of value/timestamp pairs returned as pkt4-ack-sent
// as the value for command response "Arguments" element
type ResponseArguments4 struct {
	Samples []interface{} `json:"pkt4-ack-sent"`
}

// Represents a response from the single Kea server to the statistic-get
// for pkt6-reply-sent:
//{
//    "command": "statistic-get",
//    "arguments": {
//        "pkt6-reply-sent": [
//          [ 125, "2019-07-30 10:11:19.498739" ],
//            ...
//          ]
//    },
//    "result": 0
//}
type StatGetResponse6 struct {
	agentcomm.KeaResponseHeader
	Arguments *ResponseArguments6 `json:"arguments,omitempty"`
}

// The list of value/timestamp pairs returned as pkt6-reply-sent
// as the value for command response "Arguments" element
type ResponseArguments6 struct {
	Samples []interface{} `json:"pkt6-reply-sent"`
}

// Create a RpsPuller object that in background pulls Kea RPS stats.
// Beneath it spawns a goroutine that pulls the response sent statistics
// periodically from Kea apps (that are stored in database).  These are
// used to calculate and store RPS interval data per Kea daemon in the database.
// If it is blank, the RpsPuller will not start its PeriodicPuller, allowing
// it to driven externally.  If intervalName is not blank, the RpsPuller will
// start its PeriodicPuller, using the value as the name of the db setting for
// the periodic interval.
func NewRpsPuller(db *pg.DB, agents agentcomm.ConnectedAgents, intervalName string) (*RpsPuller, error) {
	rpsPuller := &RpsPuller{}

	// Run our own polling.  Otherwise someone else is calling the shots.
	if intervalName != "" {
		periodicPuller, err := agentcomm.NewPeriodicPuller(db, agents, "Kea RPS Stats", intervalName, rpsPuller.pullStats)
		if err != nil {
			return nil, err
		}

		rpsPuller.PeriodicPuller = periodicPuller
	} else {
		// We still need a puller instance for its Db member.
		periodicPuller := &agentcomm.PeriodicPuller{
			Db: db,
		}
		rpsPuller.PeriodicPuller = periodicPuller
	}

	rpsPuller.PreviousRps = map[int64]StatSample{}
	// The interval values may some day be configurable
	rpsPuller.Interval1 = (time.Minute * 15)
	rpsPuller.Interval2 = (time.Hour * 24)
	return rpsPuller, nil
}

// Shutdown RpsPuller. It stops goroutine that pulls stats.
func (rpsPuller *RpsPuller) Shutdown() {
	if rpsPuller.PeriodicPuller.Active {
		rpsPuller.PeriodicPuller.Shutdown()
	}
}

// Pull RPS stats periodically for all Kea apps which Stork is monitoring. The function
// returns the number of apps for which the stats were successfully pulled and last
// encountered error.  This used if the RpsPuller is running it's own PeriodicPuller.
func (rpsPuller *RpsPuller) pullStats() (int, error) {
	// Delete obsolete data.
	err := rpsPuller.AgeOffRpsIntervals()
	if err != nil {
		log.Errorf("error aging of RPS interval data: %+v", err)
	}

	// Get list of all kea apps from database
	dbApps, err := dbmodel.GetAppsByType(rpsPuller.Db, dbmodel.AppTypeKea)
	if err != nil {
		return 0, err
	}

	// Get RPS stats from each kea app
	var lastErr error
	appsOkCnt := 0
	for _, dbApp := range dbApps {
		dbApp2 := dbApp
		err := rpsPuller.getStatsFromApp(&dbApp2)
		if err != nil {
			lastErr = err
			log.Errorf("error occurred while getting RPS stats from app %d: %+v", dbApp.ID, err)
		} else {
			appsOkCnt++
		}
	}

	log.Printf("completed pulling RPS stats from Kea apps: %d/%d succeeded", appsOkCnt, len(dbApps))
	return appsOkCnt, lastErr
}

// Generates RPS interval data for each daemon in a given Kea app
// This used if the RpsPuller is running it's own PeriodicPuller.
func (rpsPuller *RpsPuller) getStatsFromApp(dbApp *dbmodel.App) error {
	// Prepare URL to CA
	ctrlPoint, err := dbApp.GetAccessPoint(dbmodel.AccessPointControl)
	if err != nil {
		return err
	}

	// Slices for tracking commands, the daemons they're sent to, and the responses
	cmds := []*agentcomm.KeaCommand{}
	responses := []interface{}{}
	cmdDaemons := []*dbmodel.Daemon{}

	dhcp4Daemons := make(agentcomm.KeaDaemons)
	dhcp6Daemons := make(agentcomm.KeaDaemons)

	// Iterate over active daemons, adding commands for dhcp4 and dhcp6 daemons
	// Since we might have dhcp4 only, dhcp6 only or both, we build an array
	// of expected responses.
	for _, d := range dbApp.Daemons {
		if d.KeaDaemon != nil && d.Active {
			switch d.Name {
			case dhcp4:
				cmdDaemons = append(cmdDaemons, d)
				dhcp4Daemons[dhcp4] = true
				responses = append(responses, rpsPuller.AddCmd4(&cmds, &dhcp4Daemons))
			case dhcp6:
				cmdDaemons = append(cmdDaemons, d)
				dhcp6Daemons[dhcp6] = true
				responses = append(responses, rpsPuller.AddCmd6(&cmds, &dhcp6Daemons))
			}
		}
	}

	// If there are no commands, nothing to do
	if len(cmds) == 0 {
		return nil
	}

	// forward commands to kea
	ctx := context.Background()

	cmdsResult, err := rpsPuller.Agents.ForwardToKeaOverHTTP(ctx, dbApp.Machine.Address, dbApp.Machine.AgentPort, ctrlPoint.Address, ctrlPoint.Port, cmds, responses...)

	if err != nil {
		return err
	}

	if cmdsResult.Error != nil {
		return cmdsResult.Error
	}

	var lastErr error
	for idx := 0; idx < len(cmds); idx++ {
		switch cmdDaemons[idx].Name {
		case dhcp4:
			err = rpsPuller.Response4Handler(cmdDaemons[idx], responses[idx])
			if err != nil {
				log.Errorf("error handling statistic-get (v4) response: %+v", err)
				lastErr = err
			}
		case dhcp6:
			err = rpsPuller.Response6Handler(cmdDaemons[idx], responses[idx])
			if err != nil {
				log.Errorf("error handling statistic-get (v6) response: %+v", err)
				lastErr = err
			}
		}
	}

	return lastErr
}

// Ages off obsolete RPS interval data.
func (rpsPuller *RpsPuller) AgeOffRpsIntervals() error {
	// Age off records more than Interval2 old.
	deleteTime := storkutil.UTCNow().Add(-rpsPuller.Interval2)
	err := dbmodel.AgeOffRpsInterval(rpsPuller.Db, deleteTime)
	return err
}

// Appends the statistic-get command for DHCP4 to the given commond list. It returns
// an instance of the expected response type.
func (rpsPuller *RpsPuller) AddCmd4(cmds *[]*agentcomm.KeaCommand, dhcp4Daemons *agentcomm.KeaDaemons) interface{} {
	dhcp4Arguments := RpsGetDhcp4Arguments()
	*cmds = append(*cmds, &agentcomm.KeaCommand{
		Command:   "statistic-get",
		Daemons:   dhcp4Daemons,
		Arguments: &dhcp4Arguments})
	return (&[]StatGetResponse4{})
}

// Appends the statistic-get command for DHCP4 to the given commond list. It returns
// an instance of the expected response type.
func (rpsPuller *RpsPuller) AddCmd6(cmds *[]*agentcomm.KeaCommand, dhcp6Daemons *agentcomm.KeaDaemons) interface{} {
	dhcp6Arguments := RpsGetDhcp6Arguments()
	*cmds = append(*cmds, &agentcomm.KeaCommand{
		Command:   "statistic-get",
		Daemons:   dhcp6Daemons,
		Arguments: &dhcp6Arguments})
	return (&[]StatGetResponse6{})
}

// Processes the statistic-get command response for DHCP4.
func (rpsPuller *RpsPuller) Response4Handler(daemon *dbmodel.Daemon, response interface{}) error {
	statsResp4, ok := response.(*[]StatGetResponse4)
	if !ok {
		return fmt.Errorf("response type is invalid: %+v", response)
	}

	samples, err := rpsPuller.extractSamples4(*statsResp4)
	if err == nil {
		// Calculate and store the RPS interval for this daemon for this cycle
		err = rpsPuller.updateDaemonRpsIntervals(daemon, samples)

		// Now we'll update the Kea RPS statistics based on the updated interval data
		if err == nil {
			err = rpsPuller.updateKeaDaemonRpsStats(daemon)
		}
	}

	if err != nil {
		return errors.WithMessagef(err, "could not update dhcp4 RPS data for %+v", daemon)
	}

	return nil
}

// Processes the statistic-get command response for DHCP4.
func (rpsPuller *RpsPuller) Response6Handler(daemon *dbmodel.Daemon, response interface{}) error {
	statsResp6, ok := response.(*[]StatGetResponse6)
	if !ok {
		return fmt.Errorf("response type is invalid: %+v", response)
	}

	samples, err := rpsPuller.extractSamples6(*statsResp6)
	if err == nil {
		// Calculate and store the RPS interval for this daemon for this cycle
		err = rpsPuller.updateDaemonRpsIntervals(daemon, samples)

		// Now we'll update the Kea RPS statistics based on the updated interval data
		if err == nil {
			err = rpsPuller.updateKeaDaemonRpsStats(daemon)
		}
	}

	if err != nil {
		return errors.WithMessagef(err, "could not update dhcp4 RPS data for %+v", daemon)
	}

	return nil
}

// Exract the list of statistic samples from a dhcp4 statistic-get response if the response is valid.
func (rpsPuller *RpsPuller) extractSamples4(statsResp []StatGetResponse4) ([]interface{}, error) {
	if len(statsResp) == 0 {
		err := fmt.Errorf("empty RPS response")
		return nil, err
	}

	if statsResp[0].Result != 0 {
		err := fmt.Errorf("error result in RPS response: %+v", statsResp)
		return nil, err
	}

	if statsResp[0].Arguments == nil {
		err := fmt.Errorf("missing Arguments from RPS response %+v", statsResp)
		return nil, err
	}

	if statsResp[0].Arguments.Samples == nil {
		err := fmt.Errorf("missing Samples from RPS response: %+v", statsResp)
		return nil, err
	}

	return statsResp[0].Arguments.Samples, nil
}

// Exract the list of statistic samples from a dhcp6 statistic-get response if the response is valid.
func (rpsPuller *RpsPuller) extractSamples6(statsResp []StatGetResponse6) ([]interface{}, error) {
	if len(statsResp) == 0 {
		err := fmt.Errorf("empty RPS response")
		return nil, err
	}

	if statsResp[0].Result != 0 {
		err := fmt.Errorf("error result in RPS response: %+v", statsResp)
		return nil, err
	}

	if statsResp[0].Arguments == nil {
		err := fmt.Errorf("missing Arguments from RPS response: %+v", statsResp)
		return nil, err
	}

	if statsResp[0].Arguments.Samples == nil {
		err := fmt.Errorf("missing Samples from RPS response: %+v", statsResp)
		return nil, err
	}

	return statsResp[0].Arguments.Samples, nil
}

// Uses the most recent Kea statistic value for packets sent to calculate and
// store an RPS interval row for the current interval for the given daemon.
func (rpsPuller *RpsPuller) updateDaemonRpsIntervals(daemon *dbmodel.Daemon, samples []interface{}) error {
	// The first row of the samples is the most recent value and the only
	// one we care about. Fetch it.
	value, sampledAt, err := getFirstSample(samples)
	if err != nil {
		return errors.WithMessagef(err, "could not extract RPS statistic")
	}

	daemonID := daemon.KeaDaemon.DaemonID
	if value < 0 {
		// Shouldn't happen but if it does, we'll record a 0.
		log.Warnf("discarding response value: %d returned from KeaDaemonID: %d", value, daemonID)
		value = int64(0)
	}

	// If we have a previous recording, calculate a delta row for it
	if previous, exist := rpsPuller.PreviousRps[daemonID]; exist {
		// Make a new interval
		interval := &dbmodel.RpsInterval{}
		interval.KeaDaemonID = daemonID
		interval.StartTime = previous.SampledAt

		// Calculate the time between the two samples.
		interval.Duration = (sampledAt.Unix() - previous.SampledAt.Unix())

		// Calculate the delta in responses sent.
		if value >= previous.Value {
			// New value is larger, we assume we have contiguous data.
			interval.Responses = value - previous.Value
		} else {
			// We have either Kea restart, reset, or statistic rollover. This value
			// then represents the number packets sent since that event occurred.
			interval.Responses = value
		}

		err = dbmodel.AddRpsInterval(rpsPuller.Db, interval)
	}

	// Always update the last reported values for the Daemon.
	rpsPuller.PreviousRps[daemonID] = StatSample{sampledAt, value}

	return err
}

// Update the RPS value for both intervals for given daemon.
// Uses the RpsInterval table contents to get the total responses and duration
// for both intervals and then updates the Daemon's statistics in the db.
func (rpsPuller *RpsPuller) updateKeaDaemonRpsStats(daemon *dbmodel.Daemon) error {
	endTime := storkutil.UTCNow()
	startTime1 := endTime.Add(-rpsPuller.Interval1)
	daemonID := daemon.KeaDaemon.DaemonID

	// Fetch interval totals for interval 1.
	rps1, err := dbmodel.GetTotalRpsOverIntervalForDaemon(rpsPuller.Db, startTime1, endTime, daemonID)
	if err != nil {
		return errors.WithMessagef(err, "query for RPS interval 1 data failed")
	}

	// Calculate RPS for interval 1.
	daemon.KeaDaemon.KeaDHCPDaemon.Stats.RPS1 = calculateRps(rps1)

	// Fetch interval totals for interval 1.
	startTime2 := endTime.Add(-rpsPuller.Interval2)
	rps2, err := dbmodel.GetTotalRpsOverIntervalForDaemon(rpsPuller.Db, startTime2, endTime, daemonID)
	if err != nil {
		return errors.WithMessagef(err, "query for RPS interval 2 data failed")
	}

	// Calculate RPS for interval 2.
	daemon.KeaDaemon.KeaDHCPDaemon.Stats.RPS2 = calculateRps(rps2)

	// Update the daemon statistics.
	log.Printf("Updating KeaDHCPDaemonStats: %+v", daemon.KeaDaemon.KeaDHCPDaemon.Stats)
	return dbmodel.UpdateDaemon(rpsPuller.Db, daemon)
}

// Calculate the RPS for the first row in a set of RpsIntervals
func calculateRps(totals []*dbmodel.RpsInterval) int {
	if len(totals) == 0 {
		return 0
	}

	responses := totals[0].Responses
	duration := totals[0].Duration
	if responses <= 0 || duration <= 0 {
		return 0
	}

	// If it's a fraction, return 1 (unless we move to floats)
	if responses <= duration {
		return 1
	}

	// Return the rate.
	return (int(responses / duration))
}

// Returns the statistic value and sample time from a given row within a
// a list of samples.  Note that rather than use the sample time in the list,
// We use current Stork server time so interval times across Daemons are
// consistent and relative to us. In other words, we don't care when Kea
// modified the value, we care about when we got it.
func getFirstSample(samples []interface{}) (int64, time.Time, error) {
	sampledAt := storkutil.UTCNow()
	if samples == nil {
		return 0, sampledAt, errors.New("samples cannot be nil")
	}

	if len(samples) == 0 {
		// Not enough rows
		return 0, sampledAt, fmt.Errorf("sampleList is empty")
	}

	row, ok := samples[0].([]interface{})
	if !ok {
		return 0, sampledAt, fmt.Errorf("problem with casting sample row: %+v", samples[0])
	}

	if len(row) != 2 {
		return 0, sampledAt, fmt.Errorf("row has incorrect number of values: %+v", row)
	}

	// Not sure why unmarshalling makes it a float64, but we need an int64.
	value := int64(row[0].(float64))

	return value, sampledAt, nil
}

// "Static" constant for dhcp4 statistic-get command argument
func RpsGetDhcp4Arguments() map[string]interface{} {
	return map[string]interface{}{"name": "pkt4-ack-sent"}
}

// "Static" constant for dhcp6 statistic-get command argument
func RpsGetDhcp6Arguments() map[string]interface{} {
	return map[string]interface{}{"name": "pkt6-reply-sent"}
}
