package bind9

import (
	"testing"

	"github.com/stretchr/testify/require"

	"isc.org/stork/server/agentcomm"
	dbmodel "isc.org/stork/server/database/model"
	dbtest "isc.org/stork/server/database/test"
	storktest "isc.org/stork/server/test"
)

// Check creating and shutting down StatsPuller.
func TestStatsPullerBasic(t *testing.T) {
	// prepare db
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// set one setting that is needed by puller
	setting := dbmodel.Setting{
		Name:    "bind9_stats_puller_interval",
		ValType: dbmodel.SettingValTypeInt,
		Value:   "60",
	}
	err := db.Insert(&setting)
	require.NoError(t, err)

	// prepare fake agents
	fa := storktest.NewFakeAgents(nil, nil)

	sp, _ := NewStatsPuller(db, fa)
	sp.Shutdown()
}

// Check if pulling stats works.
func TestStatsPullerPullStats(t *testing.T) {
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// prepare fake agents
	bind9Mock := func(callNo int, statsOutput interface{}) {
		json := `{
		    "json-stats-version":"1.2",
		    "views":{
		        "_default":{
		            "resolver":{
		                "cachestats":{
		                    "CacheHits": 60,
		                    "CacheMisses": 40,
		                    "QueryHits": 10,
		                    "QueryMisses": 90
		                }
		            }
		        },
		        "_bind":{
		            "resolver":{
		                "cachestats":{
		                    "CacheHits": 30,
		                    "CacheMisses": 70,
		                    "QueryHits": 20,
		                    "QueryMisses": 80
		                }
		            }
		        }
		    }
		}`

		agentcomm.UnmarshalNamedStatsResponse(json, statsOutput)
	}
	fa := storktest.NewFakeAgents(nil, bind9Mock)

	// prepare bind9 apps
	var err error
	var accessPoints []*dbmodel.AccessPoint
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "127.0.0.1", "abcd", 953)
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointStatistics, "127.0.0.1", "abcd", 8000)

	daemon := dbmodel.Bind9Daemon{
		Pid:    0,
		Name:   "named",
		Active: true,
	}

	machine1 := &dbmodel.Machine{
		ID:        0,
		Address:   "192.0.1.0",
		AgentPort: 1111,
	}
	err = dbmodel.AddMachine(db, machine1)
	require.NoError(t, err)
	require.NotZero(t, machine1.ID)
	dbApp1 := dbmodel.App{
		Type:         dbmodel.AppTypeBind9,
		AccessPoints: accessPoints,
		MachineID:    machine1.ID,
		Details: dbmodel.AppBind9{
			Daemon: daemon,
		},
	}
	err = CommitAppIntoDB(db, &dbApp1)
	require.NoError(t, err)

	machine2 := &dbmodel.Machine{
		ID:        0,
		Address:   "192.0.2.0",
		AgentPort: 2222,
	}
	err = dbmodel.AddMachine(db, machine2)
	require.NoError(t, err)
	require.NotZero(t, machine2.ID)
	dbApp2 := dbmodel.App{
		Type:         dbmodel.AppTypeBind9,
		AccessPoints: accessPoints,
		MachineID:    machine2.ID,
		Details: dbmodel.AppBind9{
			Daemon: daemon,
		},
	}
	err = CommitAppIntoDB(db, &dbApp2)
	require.NoError(t, err)

	// set one setting that is needed by puller
	setting := dbmodel.Setting{
		Name:    "bind9_stats_puller_interval",
		ValType: dbmodel.SettingValTypeInt,
		Value:   "60",
	}
	err = db.Insert(&setting)
	require.NoError(t, err)

	// prepare stats puller
	sp, err := NewStatsPuller(db, fa)
	require.NoError(t, err)
	// shutdown stats puller at the end
	defer sp.Shutdown()

	// invoke pulling stats
	appsOkCnt, err := sp.pullStats()
	require.NoError(t, err)
	require.Equal(t, 2, appsOkCnt)

	// check collected stats
	app1, err := dbmodel.GetAppByID(db, dbApp1.ID)
	require.NoError(t, err)
	daemon = app1.Details.(dbmodel.AppBind9).Daemon
	require.EqualValues(t, 60, daemon.CacheHits)
	require.EqualValues(t, 40, daemon.CacheMisses)
	require.EqualValues(t, 0.6, daemon.CacheHitRatio)

	app2, err := dbmodel.GetAppByID(db, dbApp2.ID)
	require.NoError(t, err)
	daemon = app2.Details.(dbmodel.AppBind9).Daemon
	require.EqualValues(t, 60, daemon.CacheHits)
	require.EqualValues(t, 40, daemon.CacheMisses)
	require.EqualValues(t, 0.6, daemon.CacheHitRatio)
}

// Check if statistics-channel response is handled correctly when it is empty.
func TestStatsPullerEmptyResponse(t *testing.T) {
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// prepare fake agents
	bind9Mock := func(callNo int, statsOutput interface{}) {
		json := `{
                    "json-stats-version":"1.2"
                }`

		agentcomm.UnmarshalNamedStatsResponse(json, statsOutput)
	}
	fa := storktest.NewFakeAgents(nil, bind9Mock)

	// prepare bind9 app
	var err error
	var accessPoints []*dbmodel.AccessPoint
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "127.0.0.1", "abcd", 953)
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointStatistics, "127.0.0.1", "abcd", 8000)

	daemon := dbmodel.Bind9Daemon{
		Pid:    0,
		Name:   "named",
		Active: true,
	}

	machine := &dbmodel.Machine{
		ID:        0,
		Address:   "192.0.1.0",
		AgentPort: 1111,
	}
	err = dbmodel.AddMachine(db, machine)
	require.NoError(t, err)
	require.NotZero(t, machine.ID)
	dbApp := dbmodel.App{
		Type:         dbmodel.AppTypeBind9,
		AccessPoints: accessPoints,
		MachineID:    machine.ID,
		Details: dbmodel.AppBind9{
			Daemon: daemon,
		},
	}
	err = CommitAppIntoDB(db, &dbApp)
	require.NoError(t, err)

	// set one setting that is needed by puller
	setting := dbmodel.Setting{
		Name:    "bind9_stats_puller_interval",
		ValType: dbmodel.SettingValTypeInt,
		Value:   "60",
	}
	err = db.Insert(&setting)
	require.NoError(t, err)

	// prepare stats puller
	sp, err := NewStatsPuller(db, fa)
	require.NoError(t, err)
	// shutdown stats puller at the end
	defer sp.Shutdown()

	// invoke pulling stats
	appsOkCnt, err := sp.pullStats()
	require.NoError(t, err)
	require.Equal(t, 1, appsOkCnt)

	// check collected stats
	app1, err := dbmodel.GetAppByID(db, dbApp.ID)
	require.NoError(t, err)
	daemon = app1.Details.(dbmodel.AppBind9).Daemon
	require.EqualValues(t, 0, daemon.CacheHits)
	require.EqualValues(t, 0, daemon.CacheMisses)
	require.EqualValues(t, 0, daemon.CacheHitRatio)
}
