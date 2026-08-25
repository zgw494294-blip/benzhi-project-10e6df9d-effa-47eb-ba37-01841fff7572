package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		path = "stage-clearance.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`, `PRAGMA journal_mode = WAL`, `PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sessions (
		 id TEXT PRIMARY KEY, production_name TEXT NOT NULL, venue TEXT NOT NULL, scheduled_at TEXT NOT NULL,
		 supervisor_name TEXT NOT NULL, status TEXT NOT NULL, version INTEGER NOT NULL,
		 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, frozen_digest TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS rigging_items (
		 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), parent_item_id TEXT NOT NULL DEFAULT '',
		 item_type TEXT NOT NULL, label TEXT NOT NULL, location TEXT NOT NULL, rated_load_kg REAL NOT NULL,
		 planned_load_kg REAL NOT NULL, inspection_standard TEXT NOT NULL, revision INTEGER NOT NULL,
		 supersedes_id TEXT NOT NULL DEFAULT '', source_session_id TEXT NOT NULL DEFAULT '',
		 source_item_id TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1)`,
		`CREATE INDEX IF NOT EXISTS idx_items_session ON rigging_items(session_id, active)`,
		`CREATE TABLE IF NOT EXISTS inspections (
		 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), item_id TEXT NOT NULL REFERENCES rigging_items(id),
		 round INTEGER NOT NULL, check_type TEXT NOT NULL, measured_value TEXT NOT NULL, verdict TEXT NOT NULL,
		 evidence_ref TEXT NOT NULL, inspector_name TEXT NOT NULL, recorded_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_inspections_session ON inspections(session_id, item_id, round)`,
		`CREATE TABLE IF NOT EXISTS hazards (
		 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), inspection_id TEXT NOT NULL REFERENCES inspections(id),
		 item_id TEXT NOT NULL REFERENCES rigging_items(id), severity TEXT NOT NULL, scope TEXT NOT NULL,
		 required_action TEXT NOT NULL, assignee TEXT NOT NULL, due_at TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
		 remediation_note TEXT NOT NULL DEFAULT '', remediation_evidence TEXT NOT NULL DEFAULT '',
		 revision_item_id TEXT NOT NULL DEFAULT '', reinspection_id TEXT NOT NULL DEFAULT '', closed_at TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS idx_hazards_session ON hazards(session_id, status)`,
		`CREATE TABLE IF NOT EXISTS hazard_assignments (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, hazard_id TEXT NOT NULL REFERENCES hazards(id),
		 old_assignee TEXT NOT NULL, new_assignee TEXT NOT NULL, old_due_at TEXT NOT NULL,
		 new_due_at TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL, changed_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS remediation_records (
		 id TEXT PRIMARY KEY, hazard_id TEXT NOT NULL REFERENCES hazards(id), round INTEGER NOT NULL,
		 note TEXT NOT NULL, evidence_ref TEXT NOT NULL, actor TEXT NOT NULL, item_id TEXT NOT NULL DEFAULT '',
		 submitted_at TEXT NOT NULL, reinspection_id TEXT NOT NULL DEFAULT '', UNIQUE(hazard_id,round))`,
		`CREATE TABLE IF NOT EXISTS frozen_manifests (
		 session_id TEXT PRIMARY KEY REFERENCES sessions(id), digest TEXT NOT NULL, content BLOB NOT NULL, frozen_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS review_tokens (
		 token TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), expected_version INTEGER NOT NULL,
		 manifest_digest TEXT NOT NULL, manifest_content BLOB NOT NULL, expires_at TEXT NOT NULL,
		 reviewer TEXT NOT NULL, consumed_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS freeze_reviews (
		 session_id TEXT PRIMARY KEY REFERENCES sessions(id), token TEXT NOT NULL, preview_digest TEXT NOT NULL,
		 confirmations TEXT NOT NULL, review_note TEXT NOT NULL, reviewer TEXT NOT NULL, reviewed_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS certificates (
		 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), sequence INTEGER NOT NULL UNIQUE,
		 manifest_digest TEXT NOT NULL, previous_digest TEXT NOT NULL, certificate_digest TEXT NOT NULL UNIQUE,
		 approved_by TEXT NOT NULL, issued_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_certificates_session ON certificates(session_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, event_type TEXT NOT NULL,
		 actor TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_events(session_id, id DESC)`,
		`CREATE TABLE IF NOT EXISTS idempotency (
		 idempotency_key TEXT PRIMARY KEY, request_digest TEXT NOT NULL, response BLOB NOT NULL,
		 session_id TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`INSERT INTO schema_meta(key,value) VALUES('schemaVersion','1') ON CONFLICT(key) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("执行迁移: %w", err)
		}
	}
	if err := s.ensureColumn(ctx, "rigging_items", "source_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "rigging_items", "source_item_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "hazards", "due_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, declaration string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		found = found || name == column
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+declaration)
	return err
}
