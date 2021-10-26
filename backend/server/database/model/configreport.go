package dbmodel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-pg/pg/v9"
	"github.com/go-pg/pg/v9/orm"
	pkgerrors "github.com/pkg/errors"
	dbops "isc.org/stork/server/database"
)

// Structure representing a single config report generated during
// the daemons configuration review.
type ConfigReport struct {
	ID          int64
	CreatedAt   time.Time
	CheckerName string
	Content     string

	DaemonID int64

	RefDaemons []*Daemon `pg:"many2many:daemon_to_config_report,fk:config_report_id,joinFK:daemon_id"`
}

// Structure representing a many-to-many relationship between daemons
// and config reports.
type DaemonToConfigReport struct {
	DaemonID       int64 `pg:",pk"`
	ConfigReportID int64 `pg:",pk"`
	OrderIndex     int64
}

// Adds a single configuration report and its relationships with the
// daemons to the database.
func AddConfigReport(dbIface interface{}, configReport *ConfigReport) error {
	// Start transaction if it hasn't been started yet.
	tx, rollback, commit, err := dbops.Transaction(dbIface)
	if err != nil {
		return err
	}
	defer rollback()

	// Insert the config_report entry.
	_, err = tx.Model(configReport).Insert()

	if err == nil {
		// Insert associations between the configuration report and
		// the daemons.
		var assocs []DaemonToConfigReport
		for i := range configReport.RefDaemons {
			d := configReport.RefDaemons[i]
			assocs = append(assocs, DaemonToConfigReport{
				DaemonID:       d.ID,
				ConfigReportID: configReport.ID,
				OrderIndex:     int64(i),
			})
		}

		if len(assocs) > 0 {
			// Insert the associations.
			_, err = tx.Model(&assocs).OnConflict("DO NOTHING").Insert()
		}
	}

	if err != nil {
		// The error message is formatted differently depending on whether we
		// have one or more daemons associated with the config report.
		if len(configReport.RefDaemons) == 1 {
			err = pkgerrors.Wrapf(err, "problem with inserting the configuration report for daemon %d",
				configReport.RefDaemons[0].ID)
		} else {
			var daemonIds []string
			for _, d := range configReport.RefDaemons {
				daemonIds = append(daemonIds, fmt.Sprintf("%d", d.ID))
			}
			err = pkgerrors.Wrapf(err, "problem with inserting the configuration report for daemons %s",
				strings.Join(daemonIds, ", "))
		}
		return err
	}

	// All done.
	err = commit()
	if err != nil {
		return err
	}

	return nil
}

// Select all config reports for the specified daemon.
func GetConfigReportsByDaemonID(db *pg.DB, daemonID int64) ([]ConfigReport, error) {
	var configReports []ConfigReport
	err := db.Model(&configReports).
		Where("config_report.daemon_id = ?", daemonID).
		Relation("RefDaemons", func(q *orm.Query) (*orm.Query, error) {
			return q.Order("daemon_to_config_report.order_index ASC"), nil
		}).
		Relation("RefDaemons.App").
		Select()

	if err != nil && !errors.Is(err, pg.ErrNoRows) {
		err = pkgerrors.Wrapf(err, "problem with selecting config reports for daemon %d", daemonID)
		return configReports, err
	}
	return configReports, nil
}

// Delete all config reports for the specified daemon.
func DeleteConfigReportsByDaemonID(dbIface interface{}, daemonID int64) error {
	// Start transaction if it hasn't been started yet.
	tx, rollback, commit, err := dbops.Transaction(dbIface)
	if err != nil {
		return err
	}
	defer rollback()

	_, err = tx.Model((*ConfigReport)(nil)).
		Where("daemon_id = ?", daemonID).
		Delete()

	if err != nil && !errors.Is(err, pg.ErrNoRows) {
		err = pkgerrors.Wrapf(err, "problem with deleting config reports for daemon %d", daemonID)
		return err
	}

	err = commit()
	if err != nil {
		return err
	}

	return nil
}
