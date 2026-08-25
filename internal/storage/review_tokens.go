package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"stage-rigging-clearance/internal/domain"
)

type ReviewToken struct {
	Token           string
	SessionID       string
	ExpectedVersion int64
	ManifestDigest  string
	ManifestContent []byte
	ExpiresAt       time.Time
	Reviewer        string
	ConsumedAt      time.Time
}

func (s *Store) SaveReviewToken(ctx context.Context, token ReviewToken) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int64
	var status domain.SessionStatus
	if err = tx.QueryRowContext(ctx, `SELECT version,status FROM sessions WHERE id=?`, token.SessionID).Scan(&version, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if version != token.ExpectedVersion {
		return fmt.Errorf("%w: 当前版本为 %d", domain.ErrVersionConflict, version)
	}
	if status == domain.StatusFrozen || status == domain.StatusReleased {
		return domain.ErrFrozen
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO review_tokens(token,session_id,expected_version,manifest_digest,manifest_content,expires_at,reviewer) VALUES(?,?,?,?,?,?,?)`, token.Token, token.SessionID, token.ExpectedVersion, token.ManifestDigest, token.ManifestContent, formatTime(token.ExpiresAt), token.Reviewer)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (u *Unit) ConsumeReviewToken(token, sessionID string, expectedVersion int64, manifestDigest, confirmations, reviewNote, reviewer string) error {
	var savedSession, digest, expires, consumed string
	var version int64
	err := u.tx.QueryRow(`SELECT session_id,expected_version,manifest_digest,expires_at,consumed_at FROM review_tokens WHERE token=?`, token).Scan(&savedSession, &version, &digest, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: 复核确认令牌不存在", domain.ErrInvalidInput)
	}
	if err != nil {
		return err
	}
	if savedSession != sessionID {
		return fmt.Errorf("%w: 复核确认令牌与批次不匹配", domain.ErrInvalidState)
	}
	if version != expectedVersion {
		return fmt.Errorf("%w: 复核确认令牌绑定版本为 %d", domain.ErrVersionConflict, version)
	}
	if digest != manifestDigest {
		return fmt.Errorf("%w: 预演摘要已变化", domain.ErrInvalidState)
	}
	if consumed != "" {
		return fmt.Errorf("%w: 复核确认令牌已使用", domain.ErrInvalidState)
	}
	if !u.now.Before(parseTime(expires)) {
		return fmt.Errorf("%w: 复核确认令牌已过期", domain.ErrInvalidState)
	}
	res, err := u.tx.Exec(`UPDATE review_tokens SET consumed_at=? WHERE token=? AND consumed_at=''`, formatTime(u.now), token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	_, err = u.tx.Exec(`INSERT INTO freeze_reviews(session_id,token,preview_digest,confirmations,review_note,reviewer,reviewed_at) VALUES(?,?,?,?,?,?,?)`, sessionID, token, digest, confirmations, reviewNote, reviewer, formatTime(u.now))
	return err
}
