package persistence

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// PostgresStore is a PostgreSQL-backed domain.Store.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens a connection and ensures the provisioning_records table exists.
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres store: ping: %w", err)
	}
	if _, err := db.Exec(migrateRecords); err != nil {
		return nil, fmt.Errorf("postgres store: migrate: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

const migrateRecords = `
CREATE TABLE IF NOT EXISTS provisioning_records (
    id            BIGSERIAL PRIMARY KEY,
    plan_checksum TEXT NOT NULL DEFAULT '',
    tenant_id     TEXT NOT NULL DEFAULT '',
    component     TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL DEFAULT '',
    revision      TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    error_detail  TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_records_checksum ON provisioning_records(plan_checksum);
CREATE INDEX IF NOT EXISTS idx_records_component ON provisioning_records(component);`

func (s *PostgresStore) Save(rec domain.Record) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO provisioning_records (plan_checksum,tenant_id,component,action,revision,status,error_detail,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		rec.PlanChecksum, rec.TenantID, rec.Component, rec.Action, rec.Revision, rec.Status, rec.ErrorDetail, rec.CreatedAt,
	)
	return err
}

func (s *PostgresStore) ByChecksum(checksum string) []domain.Record {
	rows, err := s.db.Query(`SELECT plan_checksum,tenant_id,component,action,revision,status,error_detail,created_at FROM provisioning_records WHERE plan_checksum=$1 ORDER BY id`, checksum)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanRecords(rows)
}

func (s *PostgresStore) Revisions(component string) []string {
	rows, err := s.db.Query(`SELECT DISTINCT revision FROM provisioning_records WHERE component=$1 AND revision!='' ORDER BY MIN(id)`, component)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var rev string
		rows.Scan(&rev)
		out = append(out, rev)
	}
	return out
}

func (s *PostgresStore) LastRevision(component string) (string, bool) {
	var rev string
	err := s.db.QueryRow(`SELECT revision FROM provisioning_records WHERE component=$1 AND revision!='' ORDER BY id DESC LIMIT 1`, component).Scan(&rev)
	if err != nil {
		return "", false
	}
	return rev, true
}

func (s *PostgresStore) HasRevision(component, revision string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM provisioning_records WHERE component=$1 AND revision=$2`, component, revision).Scan(&n)
	return n > 0
}

func scanRecords(rows *sql.Rows) []domain.Record {
	var out []domain.Record
	for rows.Next() {
		var r domain.Record
		rows.Scan(&r.PlanChecksum, &r.TenantID, &r.Component, &r.Action, &r.Revision, &r.Status, &r.ErrorDetail, &r.CreatedAt)
		out = append(out, r)
	}
	return out
}

var _ domain.Store = (*PostgresStore)(nil)
