package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"strings"
	"time"
)

func migrateIdentity(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='patients' AND COLUMN_NAME='document_key'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE patients ADD COLUMN document_key VARCHAR(20) GENERATED ALWAYS AS (UPPER(TRIM(document_number))) STORED`); err != nil {
			var me *mysql.MySQLError
			if !errors.As(err, &me) || me.Number != 1060 {
				return err
			}
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='patients' AND INDEX_NAME='idx_document_key'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := db.ExecContext(ctx, `ALTER TABLE patients ADD INDEX idx_document_key(role,document_key)`)
		if err != nil {
			var me *mysql.MySQLError
			if !errors.As(err, &me) || me.Number != 1061 {
				return err
			}
		}
	}
	return nil
}
func uniqueDocument(ctx context.Context, tx *sql.Tx, doc, except string) error {
	doc = strings.ToUpper(strings.TrimSpace(doc))
	if doc == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO patient_document_locks(document_key) VALUES(?) ON DUPLICATE KEY UPDATE document_key=VALUES(document_key)`, doc); err != nil {
		return err
	}
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT document_key FROM patient_document_locks WHERE document_key=? FOR UPDATE`, doc).Scan(&locked); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM patients WHERE role='patient' AND document_key=? AND id<>?)`, doc, except).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrDocumentTaken
	}
	return nil
}
func (r *Repo) UpdatePatient(ctx context.Context, id, email, name, doc string, birth time.Time) error {
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := uniqueDocument(ctx, tx, doc, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE patients SET email=?,full_name=?,document_number=?,birth_date=? WHERE id=? AND role='patient'`, nullable(email), name, doc, birth, id)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return ErrEmailTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM patients WHERE id=? AND role='patient')`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return tx.Commit()
}
func (r *Repo) Dependents(ctx context.Context, id string) ([]Patient, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT `+columns+` FROM patients WHERE role='patient' AND responsible_id=? AND enabled=TRUE ORDER BY full_name,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Patient{}
	for rows.Next() {
		p, err := scanPatient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
