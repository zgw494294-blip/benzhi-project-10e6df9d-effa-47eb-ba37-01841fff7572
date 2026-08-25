package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"stage-rigging-clearance/internal/domain"
	"stage-rigging-clearance/internal/integrity"
	"stage-rigging-clearance/internal/storage"
)

type ReviewPreviewCommand struct {
	WriteMeta
}

type ReviewPreview struct {
	SessionID       string             `json:"sessionId"`
	ExpectedVersion int64              `json:"expectedVersion"`
	ManifestDigest  string             `json:"manifestDigest"`
	Manifest        json.RawMessage    `json:"manifest"`
	Readiness       domain.Readiness   `json:"readiness"`
	Load            domain.LoadSummary `json:"load"`
	ReviewToken     string             `json:"reviewToken,omitempty"`
	ExpiresAt       time.Time          `json:"expiresAt,omitempty"`
	Confirmations   []string           `json:"confirmations"`
}

func (s *Service) PreviewFreeze(ctx context.Context, sessionID string, c ReviewPreviewCommand) (ReviewPreview, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return ReviewPreview{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleReviewer); err != nil {
		return ReviewPreview{}, err
	}
	snap, err := s.store.Snapshot(ctx, sessionID)
	if err != nil {
		return ReviewPreview{}, err
	}
	if snap.Session.Version != c.ExpectedVersion {
		return ReviewPreview{}, fmt.Errorf("%w: 当前版本为 %d", domain.ErrVersionConflict, snap.Session.Version)
	}
	if err = domain.RequireMutable(snap.Session); err != nil {
		return ReviewPreview{}, err
	}
	readiness := domain.EvaluateReadiness(snap)
	digest, content, digestErr := integrity.ManifestDigest(snap)
	preview := ReviewPreview{SessionID: sessionID, ExpectedVersion: snap.Session.Version, ManifestDigest: digest, Manifest: content, Readiness: readiness, Load: domain.CalculateLoadSummary(snap.Items), Confirmations: []string{"items", "inspections", "hazards", "loads"}}
	if digestErr != nil {
		return ReviewPreview{}, digestErr
	}
	if !readiness.Ready {
		return preview, nil
	}
	preview.ReviewToken = uuid.NewString()
	preview.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	if err = s.store.SaveReviewToken(ctx, storage.ReviewToken{Token: preview.ReviewToken, SessionID: sessionID, ExpectedVersion: c.ExpectedVersion, ManifestDigest: digest, ManifestContent: content, ExpiresAt: preview.ExpiresAt, Reviewer: c.Actor}); err != nil {
		return ReviewPreview{}, err
	}
	return preview, nil
}

type CertificateSearchResult struct {
	Matches      []storage.CertificateMatch   `json:"matches"`
	Candidates   bool                         `json:"candidates"`
	Verification integrity.VerificationResult `json:"verification"`
	Locations    []string                     `json:"locations"`
	Limit        int                          `json:"limit"`
	Offset       int                          `json:"offset"`
}

func (s *Service) SearchCertificates(ctx context.Context, sequence *int64, digest, prefix string, limit, offset int) (CertificateSearchResult, error) {
	criteria := 0
	if sequence != nil {
		criteria++
	}
	if strings.TrimSpace(digest) != "" {
		criteria++
	}
	if strings.TrimSpace(prefix) != "" {
		criteria++
	}
	if criteria > 1 {
		return CertificateSearchResult{}, fmt.Errorf("%w: 序号、完整摘要和摘要前缀只能选择一种", domain.ErrInvalidInput)
	}
	matches, err := s.store.SearchCertificates(ctx, sequence, strings.TrimSpace(digest), strings.TrimSpace(prefix), limit, offset)
	if err != nil {
		return CertificateSearchResult{}, err
	}
	if len(matches) == 0 {
		return CertificateSearchResult{}, fmt.Errorf("%w: 未找到匹配的放行凭据", domain.ErrNotFound)
	}
	ledger, err := s.store.CertificateChain(ctx)
	if err != nil {
		return CertificateSearchResult{}, err
	}
	verification := integrity.VerifyLedger(ledger)
	locations := append([]string{}, verification.Errors...)
	for _, certificate := range ledger {
		snap, loadErr := s.store.Snapshot(ctx, certificate.SessionID)
		if loadErr != nil {
			locations = append(locations, fmt.Sprintf("凭据 %d 关联批次不存在", certificate.Sequence))
			continue
		}
		storedDigest, _, manifestErr := s.store.Manifest(ctx, certificate.SessionID)
		computedDigest, _, computedErr := integrity.ManifestDigest(snap)
		if manifestErr != nil || computedErr != nil || snap.Session.FrozenDigest != certificate.ManifestDigest || storedDigest != certificate.ManifestDigest || computedDigest != certificate.ManifestDigest {
			locations = append(locations, fmt.Sprintf("凭据 %d 与关联冻结摘要不一致", certificate.Sequence))
		}
	}
	verification.Errors = locations
	verification.Valid = len(locations) == 0
	sort.Slice(matches, func(i, j int) bool { return matches[i].Certificate.IssuedAt.After(matches[j].Certificate.IssuedAt) })
	return CertificateSearchResult{Matches: matches, Candidates: prefix != "" && len(matches) > 1, Verification: verification, Locations: locations, Limit: limit, Offset: offset}, nil
}
