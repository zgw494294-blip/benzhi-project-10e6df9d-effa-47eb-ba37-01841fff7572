package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"stage-rigging-clearance/internal/domain"
)

type Store struct {
	db *sql.DB

	certificateCacheMu    sync.Mutex
	certificateCacheCount int
	certificateCache      []domain.Certificate
}

type CommandMeta struct {
	IdempotencyKey  string
	RequestDigest   string
	SessionID       string
	ExpectedVersion int64
	Actor           string
}

type CommandResult struct {
	Response     []byte
	EventType    string
	EventPayload any
	SessionID    string
	BumpVersion  bool
}

type Unit struct {
	tx  *sql.Tx
	now time.Time
}

func DigestRequest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) Command(ctx context.Context, meta CommandMeta, fn func(*Unit) (CommandResult, error)) ([]byte, bool, error) {
	if len(strings.TrimSpace(meta.IdempotencyKey)) < 8 || len(meta.IdempotencyKey) > 128 {
		return nil, false, fmt.Errorf("%w: idempotencyKey 长度须为 8 至 128", domain.ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var savedDigest string
	var savedResponse []byte
	err = tx.QueryRowContext(ctx, `SELECT request_digest,response FROM idempotency WHERE idempotency_key=?`, meta.IdempotencyKey).Scan(&savedDigest, &savedResponse)
	if err == nil {
		if savedDigest != meta.RequestDigest {
			return nil, false, domain.ErrIdempotency
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return savedResponse, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if meta.SessionID != "" {
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM sessions WHERE id=?`, meta.SessionID).Scan(&version); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, false, domain.ErrNotFound
			}
			return nil, false, err
		}
		if version != meta.ExpectedVersion {
			return nil, false, fmt.Errorf("%w: 当前版本为 %d", domain.ErrVersionConflict, version)
		}
	}
	unit := &Unit{tx: tx, now: time.Now().UTC()}
	result, err := fn(unit)
	if err != nil {
		return nil, false, err
	}
	sessionID := result.SessionID
	if sessionID == "" {
		sessionID = meta.SessionID
	}
	if result.BumpVersion {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET version=version+1,updated_at=? WHERE id=?`, formatTime(unit.now), sessionID)
		if err != nil {
			return nil, false, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, false, domain.ErrNotFound
		}
	}
	payload, err := json.Marshal(result.EventPayload)
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(session_id,event_type,actor,payload,created_at) VALUES(?,?,?,?,?)`, sessionID, result.EventType, meta.Actor, string(payload), formatTime(unit.now)); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO idempotency(idempotency_key,request_digest,response,session_id,created_at) VALUES(?,?,?,?,?)`, meta.IdempotencyKey, meta.RequestDigest, result.Response, sessionID, formatTime(unit.now)); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return result.Response, false, nil
}

func (u *Unit) CreateSession(s domain.Session) error {
	_, err := u.tx.Exec(`INSERT INTO sessions(id,production_name,venue,scheduled_at,supervisor_name,status,version,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,?)`, s.ID, s.ProductionName, s.Venue, formatTime(s.ScheduledAt), s.SupervisorName, s.Status, formatTime(u.now), formatTime(u.now))
	return err
}

func (u *Unit) UpdateSession(s domain.Session) error {
	result, err := u.tx.Exec(`UPDATE sessions SET production_name=?,venue=?,scheduled_at=?,supervisor_name=? WHERE id=? AND status=?`, s.ProductionName, s.Venue, formatTime(s.ScheduledAt), s.SupervisorName, s.ID, domain.StatusDraft)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	return nil
}

func (u *Unit) MarkInspecting(sessionID string) error {
	_, err := u.tx.Exec(`UPDATE sessions SET status=? WHERE id=? AND status=?`, domain.StatusInspecting, sessionID, domain.StatusDraft)
	return err
}

func (u *Unit) Session(id string) (domain.Session, error) {
	return scanSession(u.tx.QueryRow(`SELECT id,production_name,venue,scheduled_at,supervisor_name,status,version,created_at,updated_at,frozen_digest FROM sessions WHERE id=?`, id))
}

func (u *Unit) InsertItem(i domain.RiggingItem) error {
	_, err := u.tx.Exec(`INSERT INTO rigging_items(id,session_id,parent_item_id,item_type,label,location,rated_load_kg,planned_load_kg,inspection_standard,revision,supersedes_id,source_session_id,source_item_id,active) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, i.ID, i.SessionID, i.ParentItemID, i.ItemType, i.Label, i.Location, i.RatedLoadKg, i.PlannedLoadKg, i.InspectionStandard, i.Revision, i.SupersedesID, i.SourceSessionID, i.SourceItemID, boolInt(i.Active))
	return err
}

func (u *Unit) ReplaceItem(oldID string, revised domain.RiggingItem) error {
	if _, err := u.tx.Exec(`UPDATE rigging_items SET active=0 WHERE id=? AND session_id=?`, oldID, revised.SessionID); err != nil {
		return err
	}
	return u.InsertItem(revised)
}

func (u *Unit) InsertInspection(i domain.Inspection) error {
	_, err := u.tx.Exec(`INSERT INTO inspections(id,session_id,item_id,round,check_type,measured_value,verdict,evidence_ref,inspector_name,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, i.ID, i.SessionID, i.ItemID, i.Round, i.CheckType, i.MeasuredValue, i.Verdict, i.EvidenceRef, i.InspectorName, formatTime(i.RecordedAt))
	return err
}

func (u *Unit) InsertHazard(h domain.Hazard) error {
	dueAt := ""
	if !h.DueAt.IsZero() {
		dueAt = formatTime(h.DueAt)
	}
	_, err := u.tx.Exec(`INSERT INTO hazards(id,session_id,inspection_id,item_id,severity,scope,required_action,assignee,due_at,status) VALUES(?,?,?,?,?,?,?,?,?,?)`, h.ID, h.SessionID, h.InspectionID, h.ItemID, h.Severity, h.Scope, h.RequiredAction, h.Assignee, dueAt, h.Status)
	return err
}

func (u *Unit) RemediateHazard(id, note, evidence, revisionID string) error {
	res, err := u.tx.Exec(`UPDATE hazards SET status=?,remediation_note=?,remediation_evidence=?,revision_item_id=? WHERE id=? AND status=?`, domain.HazardAwaitingReinspection, note, evidence, revisionID, id, domain.HazardOpen)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	return nil
}

func (u *Unit) CloseHazard(id, reinspectionID string) error {
	res, err := u.tx.Exec(`UPDATE hazards SET status=?,reinspection_id=?,closed_at=? WHERE id=? AND status=?`, domain.HazardClosed, reinspectionID, formatTime(u.now), id, domain.HazardAwaitingReinspection)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	return nil
}

func (u *Unit) Hazard(id string) (domain.Hazard, error) {
	return scanHazard(u.tx.QueryRow(`SELECT id,session_id,inspection_id,item_id,severity,scope,required_action,assignee,due_at,status,remediation_note,remediation_evidence,revision_item_id,reinspection_id,closed_at FROM hazards WHERE id=?`, id))
}

func (u *Unit) Freeze(sessionID, digest string, content []byte) error {
	if _, err := u.tx.Exec(`INSERT INTO frozen_manifests(session_id,digest,content,frozen_at) VALUES(?,?,?,?)`, sessionID, digest, content, formatTime(u.now)); err != nil {
		return err
	}
	res, err := u.tx.Exec(`UPDATE sessions SET status=?,frozen_digest=? WHERE id=? AND status IN (?,?)`, domain.StatusFrozen, digest, sessionID, domain.StatusDraft, domain.StatusInspecting)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	return nil
}

func (u *Unit) InsertCertificate(c domain.Certificate) error {
	if _, err := u.tx.Exec(`INSERT INTO certificates(id,session_id,sequence,manifest_digest,previous_digest,certificate_digest,approved_by,issued_at) VALUES(?,?,?,?,?,?,?,?)`, c.ID, c.SessionID, c.Sequence, c.ManifestDigest, c.PreviousDigest, c.CertificateDigest, c.ApprovedBy, formatTime(c.IssuedAt)); err != nil {
		return err
	}
	res, err := u.tx.Exec(`UPDATE sessions SET status=? WHERE id=? AND status=?`, domain.StatusReleased, c.SessionID, domain.StatusFrozen)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	return nil
}

func (u *Unit) NextCertificate() (int64, string, error) {
	var seq sql.NullInt64
	var digest sql.NullString
	err := u.tx.QueryRow(`SELECT sequence,certificate_digest FROM certificates ORDER BY sequence DESC LIMIT 1`).Scan(&seq, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return seq.Int64 + 1, digest.String, nil
}

func (u *Unit) Snapshot(id string) (domain.Snapshot, error) { return loadSnapshot(u.tx, id) }

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func (s *Store) Snapshot(ctx context.Context, id string) (domain.Snapshot, error) {
	return loadSnapshot(s.db, id)
}

func loadSnapshot(q querier, id string) (domain.Snapshot, error) {
	var out domain.Snapshot
	var err error
	out.Session, err = scanSession(q.QueryRow(`SELECT id,production_name,venue,scheduled_at,supervisor_name,status,version,created_at,updated_at,frozen_digest FROM sessions WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	rows, err := q.Query(`SELECT id,session_id,parent_item_id,item_type,label,location,rated_load_kg,planned_load_kg,inspection_standard,revision,supersedes_id,source_session_id,source_item_id,active FROM rigging_items WHERE session_id=? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.RiggingItem
		var active int
		if err := rows.Scan(&v.ID, &v.SessionID, &v.ParentItemID, &v.ItemType, &v.Label, &v.Location, &v.RatedLoadKg, &v.PlannedLoadKg, &v.InspectionStandard, &v.Revision, &v.SupersedesID, &v.SourceSessionID, &v.SourceItemID, &active); err != nil {
			rows.Close()
			return out, err
		}
		v.Active = active == 1
		out.Items = append(out.Items, v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT id,session_id,item_id,round,check_type,measured_value,verdict,evidence_ref,inspector_name,recorded_at FROM inspections WHERE session_id=? ORDER BY recorded_at,id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.Inspection
		var at string
		if err := rows.Scan(&v.ID, &v.SessionID, &v.ItemID, &v.Round, &v.CheckType, &v.MeasuredValue, &v.Verdict, &v.EvidenceRef, &v.InspectorName, &at); err != nil {
			rows.Close()
			return out, err
		}
		v.RecordedAt = parseTime(at)
		out.Inspections = append(out.Inspections, v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT id,session_id,inspection_id,item_id,severity,scope,required_action,assignee,due_at,status,remediation_note,remediation_evidence,revision_item_id,reinspection_id,closed_at FROM hazards WHERE session_id=? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		v, err := scanHazard(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Hazards = append(out.Hazards, v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT r.id,r.hazard_id,r.round,r.note,r.evidence_ref,r.actor,r.item_id,r.submitted_at,r.reinspection_id FROM remediation_records r JOIN hazards h ON h.id=r.hazard_id WHERE h.session_id=? ORDER BY r.submitted_at,r.id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.RemediationRecord
		var at string
		if err := rows.Scan(&v.ID, &v.HazardID, &v.Round, &v.Note, &v.EvidenceRef, &v.Actor, &v.ItemID, &at, &v.ReinspectionID); err != nil {
			rows.Close()
			return out, err
		}
		v.SubmittedAt = parseTime(at)
		out.Remediations = append(out.Remediations, v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT a.id,a.hazard_id,a.old_assignee,a.new_assignee,a.old_due_at,a.new_due_at,a.actor,a.reason,a.changed_at FROM hazard_assignments a JOIN hazards h ON h.id=a.hazard_id WHERE h.session_id=? ORDER BY a.id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.HazardAssignment
		var oldDue, newDue, at string
		if err := rows.Scan(&v.ID, &v.HazardID, &v.OldAssignee, &v.NewAssignee, &oldDue, &newDue, &v.Actor, &v.Reason, &at); err != nil {
			rows.Close()
			return out, err
		}
		v.OldDueAt, v.NewDueAt, v.ChangedAt = parseTime(oldDue), parseTime(newDue), parseTime(at)
		out.Assignments = append(out.Assignments, v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT id,session_id,sequence,manifest_digest,previous_digest,certificate_digest,approved_by,issued_at FROM certificates WHERE session_id=? ORDER BY sequence`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.Certificate
		var at string
		if err := rows.Scan(&v.ID, &v.SessionID, &v.Sequence, &v.ManifestDigest, &v.PreviousDigest, &v.CertificateDigest, &v.ApprovedBy, &at); err != nil {
			rows.Close()
			return out, err
		}
		v.IssuedAt = parseTime(at)
		out.Certificates = append(out.Certificates, v)
	}
	rows.Close()
	return out, nil
}

type rowScanner interface{ Scan(...any) error }

func scanSession(row rowScanner) (domain.Session, error) {
	var v domain.Session
	var scheduled, created, updated string
	err := row.Scan(&v.ID, &v.ProductionName, &v.Venue, &scheduled, &v.SupervisorName, &v.Status, &v.Version, &created, &updated, &v.FrozenDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.ScheduledAt = parseTime(scheduled)
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	return v, nil
}
func scanHazard(row rowScanner) (domain.Hazard, error) {
	var v domain.Hazard
	var due, closed string
	err := row.Scan(&v.ID, &v.SessionID, &v.InspectionID, &v.ItemID, &v.Severity, &v.Scope, &v.RequiredAction, &v.Assignee, &due, &v.Status, &v.RemediationNote, &v.RemediationEvidence, &v.RevisionItemID, &v.ReinspectionID, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if closed != "" {
		t := parseTime(closed)
		v.ClosedAt = &t
	}
	if due != "" {
		v.DueAt = parseTime(due)
	}
	return v, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,production_name,venue,scheduled_at,supervisor_name,status,version,created_at,updated_at,frozen_digest FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		v, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Audit(ctx context.Context, sessionID string, limit, offset int) ([]domain.AuditEvent, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,event_type,actor,payload,created_at FROM audit_events WHERE session_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, sessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var v domain.AuditEvent
		var at string
		if err := rows.Scan(&v.ID, &v.SessionID, &v.EventType, &v.Actor, &v.Payload, &at); err != nil {
			return nil, err
		}
		v.CreatedAt = parseTime(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Manifest(ctx context.Context, sessionID string) (string, []byte, error) {
	var d string
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT digest,content FROM frozen_manifests WHERE session_id=?`, sessionID).Scan(&d, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, domain.ErrNotFound
	}
	return d, b, err
}

func (s *Store) CertificateChain(ctx context.Context) ([]domain.Certificate, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificates`).Scan(&count); err != nil {
		return nil, err
	}
	s.certificateCacheMu.Lock()
	if s.certificateCache != nil && s.certificateCacheCount == count {
		cached := append([]domain.Certificate(nil), s.certificateCache...)
		s.certificateCacheMu.Unlock()
		return cached, nil
	}
	s.certificateCacheMu.Unlock()

	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,sequence,manifest_digest,previous_digest,certificate_digest,approved_by,issued_at FROM certificates ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certificates []domain.Certificate
	for rows.Next() {
		var certificate domain.Certificate
		var issuedAt string
		if err := rows.Scan(&certificate.ID, &certificate.SessionID, &certificate.Sequence, &certificate.ManifestDigest, &certificate.PreviousDigest, &certificate.CertificateDigest, &certificate.ApprovedBy, &issuedAt); err != nil {
			return nil, err
		}
		certificate.IssuedAt = parseTime(issuedAt)
		certificates = append(certificates, certificate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.certificateCacheMu.Lock()
	s.certificateCacheCount = count
	s.certificateCache = append([]domain.Certificate(nil), certificates...)
	s.certificateCacheMu.Unlock()
	return certificates, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key='schemaVersion'`).Scan(&v); err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(s string) time.Time  { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
