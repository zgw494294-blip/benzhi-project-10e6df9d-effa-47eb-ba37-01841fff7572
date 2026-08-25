package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"stage-rigging-clearance/internal/domain"
)

var certificateStatementCache = struct {
	sync.Mutex
	statements map[string]*sql.Stmt
}{statements: make(map[string]*sql.Stmt)}

type CertificateMatch struct {
	Certificate domain.Certificate `json:"certificate"`
	Session     domain.Session     `json:"session"`
}

func (s *Store) SearchCertificates(ctx context.Context, sequence *int64, digest, prefix string, limit, offset int) ([]CertificateMatch, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	where, arg := "1=1", any(nil)
	if sequence != nil {
		if *sequence < 1 {
			return nil, fmt.Errorf("%w: 凭据序号必须为正整数", domain.ErrInvalidInput)
		}
		where, arg = "c.sequence=?", *sequence
	} else if digest != "" {
		if len(digest) != 64 || !isHex(digest) {
			return nil, fmt.Errorf("%w: 凭据摘要必须为 64 位十六进制", domain.ErrInvalidInput)
		}
		where, arg = "c.certificate_digest=?", strings.ToLower(digest)
	} else if prefix != "" {
		if len(prefix) < 4 || len(prefix) > 63 || !isHex(prefix) {
			return nil, fmt.Errorf("%w: 摘要前缀须为 4 至 63 位十六进制", domain.ErrInvalidInput)
		}
		where, arg = "c.certificate_digest LIKE ?", strings.ToLower(prefix)+"%"
	}
	query := `SELECT c.id,c.session_id,c.sequence,c.manifest_digest,c.previous_digest,c.certificate_digest,c.approved_by,c.issued_at,s.id,s.production_name,s.venue,s.scheduled_at,s.supervisor_name,s.status,s.version,s.created_at,s.updated_at,s.frozen_digest FROM certificates c JOIN sessions s ON s.id=c.session_id WHERE ` + where + ` ORDER BY c.issued_at DESC,c.sequence DESC LIMIT ? OFFSET ?`
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	args = append(args, limit, offset)
	certificateStatementCache.Lock()
	statement := certificateStatementCache.statements[query]
	if statement == nil {
		var err error
		statement, err = s.db.PrepareContext(ctx, query)
		if err != nil {
			certificateStatementCache.Unlock()
			return nil, err
		}
		certificateStatementCache.statements[query] = statement
	}
	certificateStatementCache.Unlock()
	rows, err := statement.QueryContext(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CertificateMatch
	for rows.Next() {
		var m CertificateMatch
		var issued, scheduled, created, updated string
		if err := rows.Scan(&m.Certificate.ID, &m.Certificate.SessionID, &m.Certificate.Sequence, &m.Certificate.ManifestDigest, &m.Certificate.PreviousDigest, &m.Certificate.CertificateDigest, &m.Certificate.ApprovedBy, &issued, &m.Session.ID, &m.Session.ProductionName, &m.Session.Venue, &scheduled, &m.Session.SupervisorName, &m.Session.Status, &m.Session.Version, &created, &updated, &m.Session.FrozenDigest); err != nil {
			return nil, err
		}
		m.Certificate.IssuedAt = parseTime(issued)
		m.Session.ScheduledAt, m.Session.CreatedAt, m.Session.UpdatedAt = parseTime(scheduled), parseTime(created), parseTime(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

func isHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return value != ""
}
