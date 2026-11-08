// Package repository is the data layer of Pacientes (MySQL/InnoDB, ADR-003).
package repository

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound    = errors.New("patient not found")
	ErrEmailTaken  = errors.New("email already registered")
	ErrDuplicateID = errors.New("patient id already exists")
)

// Patient is a stored patient. Empty strings and a zero BirthDate mean NULL:
// a minor may have no email or password and only a responsible adult.
type Patient struct {
	ID             string
	Email          string
	PasswordHash   string
	FullName       string
	DocumentNumber string
	BirthDate      time.Time
	Enabled        bool
	ResponsibleID  string
	CreatedAt      time.Time
}

type Repo struct{ DB *sql.DB }

// Open connects and applies the idempotent schema (CREATE TABLE IF NOT EXISTS).
func Open(ctx context.Context, dsn string) (*Repo, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ParseTime, cfg.Loc, cfg.MultiStatements = true, time.UTC, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Repo{DB: db}, nil
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// Create inserts a patient. The unique index on email resolves concurrent
// registrations: the loser receives ErrEmailTaken.
func (r *Repo) Create(ctx context.Context, p Patient) error {
	var birth sql.NullTime
	if !p.BirthDate.IsZero() {
		birth = sql.NullTime{Time: p.BirthDate, Valid: true}
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO patients
		(id, email, password_hash, full_name, document_number, birth_date, enabled, responsible_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, nullable(p.Email), nullable(p.PasswordHash), p.FullName, nullable(p.DocumentNumber), birth, p.Enabled,
		nullable(p.ResponsibleID), p.CreatedAt)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		if strings.Contains(me.Message, "uq_email") {
			return ErrEmailTaken
		}
		return ErrDuplicateID
	}
	return err
}

const columns = "id, email, password_hash, full_name, document_number, birth_date, enabled, responsible_id, created_at"

func scan(row *sql.Row) (Patient, error) {
	var p Patient
	var email, hash, doc, responsible sql.NullString
	var birth sql.NullTime
	err := row.Scan(&p.ID, &email, &hash, &p.FullName, &doc, &birth, &p.Enabled, &responsible, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Patient{}, ErrNotFound
	}
	p.Email, p.PasswordHash, p.DocumentNumber, p.ResponsibleID = email.String, hash.String, doc.String, responsible.String
	if birth.Valid {
		p.BirthDate = birth.Time
	}
	return p, err
}

func (r *Repo) ByEmail(ctx context.Context, email string) (Patient, error) {
	return scan(r.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM patients WHERE email = ?", email))
}

func (r *Repo) ByID(ctx context.Context, id string) (Patient, error) {
	return scan(r.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM patients WHERE id = ?", id))
}

func (r *Repo) SetPasswordHash(ctx context.Context, id, hash string) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE patients SET password_hash = ? WHERE id = ?", hash, id)
	return err
}

func (r *Repo) Ping(ctx context.Context) error { return r.DB.PingContext(ctx) }
