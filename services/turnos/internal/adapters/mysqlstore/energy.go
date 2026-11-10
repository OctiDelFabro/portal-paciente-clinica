package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Consultation energy columns (ADR-009). DECIMAL keeps kWh and ARS exact;
// they are read as text and converted to integer micro-kWh and cents.
const energyColumns = "COALESCE(CAST(energy_kwh AS CHAR), ''), COALESCE(CAST(energy_cost_ars AS CHAR), ''), energy_status, COALESCE(energy_algorithm_version, '')"

// energyDDL is an additive migration: new nullable columns plus a status that
// defaults to pending, so rows written before it stay valid and get estimated
// by the re-estimator. MySQL 8.4 has no ADD COLUMN IF NOT EXISTS, so each
// column is checked in information_schema first.
var energyDDL = []struct{ column, ddl string }{
	{"base_price_cents", "ALTER TABLE appointments ADD COLUMN base_price_cents BIGINT NULL"},
	{"energy_tariff_cents_per_kwh", "ALTER TABLE appointments ADD COLUMN energy_tariff_cents_per_kwh BIGINT NULL"},
	{"energy_kwh", "ALTER TABLE appointments ADD COLUMN energy_kwh DECIMAL(16,6) NULL"},
	{"energy_cost_ars", "ALTER TABLE appointments ADD COLUMN energy_cost_ars DECIMAL(14,2) NULL"},
	{"energy_status", "ALTER TABLE appointments ADD COLUMN energy_status VARCHAR(16) NOT NULL DEFAULT 'pending'"},
	{"energy_algorithm_version", "ALTER TABLE appointments ADD COLUMN energy_algorithm_version VARCHAR(32) NULL"},
	{"energy_attempted_at", "ALTER TABLE appointments ADD COLUMN energy_attempted_at DATETIME(3) NULL"},
}

const energyIndex = "ALTER TABLE appointments ADD INDEX idx_energy_pending (energy_status, energy_attempted_at)"

// ignorable: another instance applied the same change concurrently
// (1060 duplicate column, 1061 duplicate key name).
func ignorable(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1060 || me.Number == 1061)
}

func migrateEnergy(ctx context.Context, db *sql.DB) error {
	for _, c := range energyDDL {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'appointments' AND COLUMN_NAME = ?`, c.column).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, c.ddl); err != nil {
			if ignorable(err) {
				continue
			}
			return fmt.Errorf("%s: %w", c.column, err)
		}
		if c.column == "energy_status" {
			// Partner bookings bill a flat price (ADR-008): never estimated.
			if _, err := db.ExecContext(ctx, `UPDATE appointments SET energy_status = 'not_applicable'
				WHERE id IN (SELECT appointment_id FROM partner_bookings)`); err != nil {
				return err
			}
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'appointments' AND INDEX_NAME = 'idx_energy_pending'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.ExecContext(ctx, energyIndex); err != nil && !ignorable(err) {
			return err
		}
	}
	return nil
}

type energyRow struct{ kwh, cost, status, version string }

func (r energyRow) energy() (domain.Energy, error) {
	e := domain.Energy{Status: domain.EnergyStatus(r.status)}
	if e.Status != domain.EnergyEstimated {
		return e, nil
	}
	var err1, err2 error
	e.MicroKWh, _, err1 = domain.ParseScaled(r.kwh, 6)
	e.CostCents, _, err2 = domain.ParseScaled(r.cost, 2)
	e.AlgorithmVersion = r.version
	if err := errors.Join(err1, err2); err != nil {
		return e, fmt.Errorf("energy columns: %w", err)
	}
	return e, nil
}

func energyStatus(e domain.Energy) string {
	if e.Status == "" {
		return string(domain.EnergyPending)
	}
	return string(e.Status)
}

// energyArgs are the nullable kWh, cost and version of an estimated energy.
func energyArgs(e domain.Energy) (kwh, cost, version any) {
	if e.Status != domain.EnergyEstimated {
		return nil, nil, nil
	}
	return domain.FormatScaled(e.MicroKWh, 6), domain.FormatScaled(e.CostCents, 2), e.AlgorithmVersion
}

// SaveEnergy writes only over a pending row, so a late estimate never
// overwrites a final one; it does not touch status or version.
func (s *Store) SaveEnergy(ctx context.Context, id string, e domain.Energy, attemptedAt time.Time) error {
	if e.Status != domain.EnergyEstimated {
		_, err := s.DB.ExecContext(ctx, `UPDATE appointments SET energy_attempted_at = ?
			WHERE id = ? AND energy_status = 'pending'`, attemptedAt.UTC(), id)
		return err
	}
	kwh, cost, version := energyArgs(e)
	_, err := s.DB.ExecContext(ctx, `UPDATE appointments SET energy_kwh = ?, energy_cost_ars = ?,
		energy_status = 'estimated', energy_algorithm_version = ?, energy_attempted_at = ?
		WHERE id = ? AND energy_status = 'pending'`, kwh, cost, version, attemptedAt.UTC(), id)
	return err
}

// PendingEnergy lists future booked appointments still pending, never
// attempted first, then least recently attempted, so one failing row cannot
// starve the others. Uses idx_energy_pending.
func (s *Store) PendingEnergy(ctx context.Context, now time.Time, limit int) ([]domain.Appointment, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+` FROM appointments
		WHERE energy_status = 'pending' AND status = 'booked' AND start_at > ?
		ORDER BY energy_attempted_at IS NOT NULL, energy_attempted_at, created_at LIMIT ?`, now.UTC(), limit)
	return scanAll(rows, err)
}
