package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"stage-rigging-clearance/internal/domain"
	"stage-rigging-clearance/internal/integrity"
	"stage-rigging-clearance/internal/storage"
)

type inspectionPreflightCache struct {
	mu        sync.Mutex
	sessionID string
	result    BatchInspectionPreflight
}

type Service struct {
	store                    *storage.Store
	inspectionPreflightCache inspectionPreflightCache
}

func New(store *storage.Store) *Service { return &Service{store: store} }

type WriteMeta struct {
	ExpectedVersion int64       `json:"expectedVersion"`
	IdempotencyKey  string      `json:"idempotencyKey"`
	Actor           string      `json:"actor"`
	Role            domain.Role `json:"role"`
}

func (m WriteMeta) validate() error {
	if strings.TrimSpace(m.Actor) == "" {
		return fmt.Errorf("%w: actor 不能为空", domain.ErrInvalidInput)
	}
	if len(strings.TrimSpace(m.IdempotencyKey)) < 8 {
		return fmt.Errorf("%w: idempotencyKey 至少 8 个字符", domain.ErrInvalidInput)
	}
	return nil
}

type CreateSessionCommand struct {
	WriteMeta
	ProductionName  string    `json:"productionName"`
	Venue           string    `json:"venue"`
	ScheduledAt     time.Time `json:"scheduledAt"`
	SupervisorName  string    `json:"supervisorName"`
	SourceSessionID string    `json:"sourceSessionId,omitempty"`
	SelectedItemIDs []string  `json:"selectedItemIds,omitempty"`
}

func (s *Service) CreateSession(ctx context.Context, c CreateSessionCommand) (domain.Session, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.Session{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return domain.Session{}, err
	}
	now := time.Now().UTC()
	session := domain.Session{ID: uuid.NewString(), ProductionName: strings.TrimSpace(c.ProductionName), Venue: strings.TrimSpace(c.Venue), ScheduledAt: c.ScheduledAt, SupervisorName: strings.TrimSpace(c.SupervisorName), Status: domain.StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := domain.ValidateSession(session); err != nil {
		return domain.Session{}, err
	}
	b, _, err := s.store.Command(ctx, storage.CommandMeta{IdempotencyKey: c.IdempotencyKey, RequestDigest: storage.DigestRequest(c), Actor: c.Actor}, func(u *storage.Unit) (storage.CommandResult, error) {
		var copied []domain.RiggingItem
		if c.SourceSessionID != "" {
			source, err := u.Snapshot(c.SourceSessionID)
			if err != nil {
				return storage.CommandResult{}, err
			}
			if source.Session.Status != domain.StatusReleased {
				return storage.CommandResult{}, fmt.Errorf("%w: 仅已放行批次可作为方案来源", domain.ErrInvalidState)
			}
			selected := map[string]bool{}
			for _, id := range c.SelectedItemIDs {
				selected[id] = true
			}
			if len(selected) == 0 {
				return storage.CommandResult{}, fmt.Errorf("%w: 至少选择一个来源构件", domain.ErrInvalidInput)
			}
			newIDs := map[string]string{}
			for _, item := range source.Items {
				if item.Active && selected[item.ID] {
					newIDs[item.ID] = uuid.NewString()
				}
			}
			if len(newIDs) != len(selected) {
				return storage.CommandResult{}, fmt.Errorf("%w: 来源构件不存在或已停用", domain.ErrInvalidInput)
			}
			for _, item := range source.Items {
				newID, ok := newIDs[item.ID]
				if !ok {
					continue
				}
				if item.ParentItemID != "" && newIDs[item.ParentItemID] == "" {
					return storage.CommandResult{}, fmt.Errorf("%w: 已选择子构件 %s，但其父构件未选择", domain.ErrInvalidState, item.Label)
				}
				item.ID, item.SessionID = newID, session.ID
				item.ParentItemID = newIDs[item.ParentItemID]
				item.Revision, item.SupersedesID = 1, ""
				item.SourceSessionID, item.SourceItemID = source.Session.ID, findSourceID(newIDs, newID)
				copied = append(copied, item)
			}
			if err := domain.ValidateGroupLoads(copied); err != nil {
				return storage.CommandResult{}, err
			}
		}
		if err := u.CreateSession(session); err != nil {
			return storage.CommandResult{}, err
		}
		if err := u.InsertItems(copied); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(session)
		payload := any(session)
		event := "session.created"
		if c.SourceSessionID != "" {
			event = "session.created_from_released_plan"
			payload = map[string]any{"session": session, "sourceSessionId": c.SourceSessionID, "copiedItems": copied}
		}
		return storage.CommandResult{Response: response, EventType: event, EventPayload: payload, SessionID: session.ID}, nil
	})
	if err != nil {
		return domain.Session{}, err
	}
	var out domain.Session
	err = json.Unmarshal(b, &out)
	return out, err
}

func findSourceID(ids map[string]string, newID string) string {
	for sourceID, generatedID := range ids {
		if generatedID == newID {
			return sourceID
		}
	}
	return ""
}

type UpdateSessionCommand struct {
	WriteMeta
	ProductionName string    `json:"productionName"`
	Venue          string    `json:"venue"`
	ScheduledAt    time.Time `json:"scheduledAt"`
	SupervisorName string    `json:"supervisorName"`
}

func (s *Service) UpdateSession(ctx context.Context, sessionID string, c UpdateSessionCommand) (domain.Session, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.Session{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return domain.Session{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		current, err := u.Session(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(current); err != nil {
			return storage.CommandResult{}, err
		}
		updated := current
		updated.ProductionName = strings.TrimSpace(c.ProductionName)
		updated.Venue = strings.TrimSpace(c.Venue)
		updated.ScheduledAt = c.ScheduledAt
		updated.SupervisorName = strings.TrimSpace(c.SupervisorName)
		updated.Version = c.ExpectedVersion + 1
		updated.UpdatedAt = time.Now().UTC()
		if err = domain.ValidateSession(updated); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.UpdateSession(updated); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(updated)
		return mutation(response, "session.updated", updated), nil
	})
	if err != nil {
		return domain.Session{}, err
	}
	var out domain.Session
	err = json.Unmarshal(b, &out)
	return out, err
}

type AddItemCommand struct {
	WriteMeta
	ParentItemID       string          `json:"parentItemId"`
	ItemType           domain.ItemType `json:"itemType"`
	Label              string          `json:"label"`
	Location           string          `json:"location"`
	RatedLoadKg        float64         `json:"ratedLoadKg"`
	PlannedLoadKg      float64         `json:"plannedLoadKg"`
	InspectionStandard string          `json:"inspectionStandard"`
}

func (s *Service) AddItem(ctx context.Context, sessionID string, c AddItemCommand) (domain.RiggingItem, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.RiggingItem{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return domain.RiggingItem{}, err
	}
	item := domain.RiggingItem{ID: uuid.NewString(), SessionID: sessionID, ParentItemID: c.ParentItemID, ItemType: c.ItemType, Label: strings.TrimSpace(c.Label), Location: strings.TrimSpace(c.Location), RatedLoadKg: c.RatedLoadKg, PlannedLoadKg: c.PlannedLoadKg, InspectionStandard: strings.TrimSpace(c.InspectionStandard), Revision: 1, Active: true}
	if err := domain.ValidateItem(item); err != nil {
		return item, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if snap.Session.Status != domain.StatusDraft {
			return storage.CommandResult{}, fmt.Errorf("%w: 仅草拟批次可登记构件", domain.ErrInvalidState)
		}
		items := append(append([]domain.RiggingItem{}, snap.Items...), item)
		if err = domain.ValidateGroupLoads(items); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.InsertItem(item); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(item)
		return mutation(response, "item.added", item), nil
	})
	if err != nil {
		return domain.RiggingItem{}, err
	}
	var out domain.RiggingItem
	err = json.Unmarshal(b, &out)
	return out, err
}

type SubmitInspectionCommand struct {
	WriteMeta
	ItemID         string           `json:"itemId"`
	CheckType      domain.CheckType `json:"checkType"`
	MeasuredValue  string           `json:"measuredValue"`
	Verdict        domain.Verdict   `json:"verdict"`
	EvidenceRef    string           `json:"evidenceRef"`
	InspectorName  string           `json:"inspectorName"`
	Severity       domain.Severity  `json:"severity,omitempty"`
	Scope          string           `json:"scope,omitempty"`
	RequiredAction string           `json:"requiredAction,omitempty"`
	Assignee       string           `json:"assignee,omitempty"`
	DueAt          time.Time        `json:"dueAt,omitempty"`
}
type InspectionResult struct {
	Inspection domain.Inspection `json:"inspection"`
	Hazard     *domain.Hazard    `json:"hazard,omitempty"`
	Version    int64             `json:"version"`
}

func (s *Service) SubmitInspection(ctx context.Context, sessionID string, c SubmitInspectionCommand) (InspectionResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return InspectionResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleTechnician); err != nil {
		return InspectionResult{}, err
	}
	in := domain.Inspection{ID: uuid.NewString(), SessionID: sessionID, ItemID: c.ItemID, Round: 1, CheckType: c.CheckType, MeasuredValue: strings.TrimSpace(c.MeasuredValue), Verdict: c.Verdict, EvidenceRef: strings.TrimSpace(c.EvidenceRef), InspectorName: strings.TrimSpace(c.InspectorName), RecordedAt: time.Now().UTC()}
	if err := domain.ValidateInspection(in, false); err != nil {
		return InspectionResult{}, err
	}
	if in.Verdict == domain.VerdictFail {
		if c.Severity != domain.SeverityBlocking && c.Severity != domain.SeverityAdvisory {
			return InspectionResult{}, fmt.Errorf("%w: 不合格项必须指定隐患级别", domain.ErrInvalidInput)
		}
		if strings.TrimSpace(c.Scope) == "" || strings.TrimSpace(c.RequiredAction) == "" || strings.TrimSpace(c.Assignee) == "" {
			return InspectionResult{}, fmt.Errorf("%w: 不合格项必须填写影响范围、处置要求和责任人", domain.ErrInvalidInput)
		}
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(snap.Session); err != nil {
			return storage.CommandResult{}, err
		}
		found := false
		lineage := map[string]bool{in.ItemID: true}
		byID := map[string]domain.RiggingItem{}
		for _, item := range snap.Items {
			byID[item.ID] = item
		}
		for _, item := range snap.Items {
			if item.ID == in.ItemID && item.Active {
				found = true
				for current := item; current.SupersedesID != ""; {
					lineage[current.SupersedesID] = true
					current = byID[current.SupersedesID]
				}
			}
		}
		if !found {
			return storage.CommandResult{}, fmt.Errorf("%w: 当前构件不存在", domain.ErrNotFound)
		}
		for _, old := range snap.Inspections {
			if lineage[old.ItemID] && old.Round == 1 && old.CheckType == in.CheckType {
				return storage.CommandResult{}, fmt.Errorf("%w: 本轮该检查项已经提交", domain.ErrInvalidState)
			}
		}
		if err = u.InsertInspection(in); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.MarkInspecting(sessionID); err != nil {
			return storage.CommandResult{}, err
		}
		result := InspectionResult{Inspection: in, Version: c.ExpectedVersion + 1}
		if in.Verdict == domain.VerdictFail {
			if err = domain.ValidateHazardDeadline(c.Assignee, c.DueAt, snap.Session.ScheduledAt, time.Now().UTC()); err != nil {
				return storage.CommandResult{}, err
			}
			h := domain.Hazard{ID: uuid.NewString(), SessionID: sessionID, InspectionID: in.ID, ItemID: in.ItemID, Severity: c.Severity, Scope: strings.TrimSpace(c.Scope), RequiredAction: strings.TrimSpace(c.RequiredAction), Assignee: strings.TrimSpace(c.Assignee), DueAt: c.DueAt.UTC(), Status: domain.HazardOpen}
			if err = u.InsertHazard(h); err != nil {
				return storage.CommandResult{}, err
			}
			result.Hazard = &h
		}
		response, _ := json.Marshal(result)
		return mutation(response, "inspection.recorded", result), nil
	})
	if err != nil {
		return InspectionResult{}, err
	}
	var out InspectionResult
	err = json.Unmarshal(b, &out)
	return out, err
}

type RemediateCommand struct {
	WriteMeta
	Note               string  `json:"note"`
	EvidenceRef        string  `json:"evidenceRef"`
	ReviseItem         bool    `json:"reviseItem"`
	Label              string  `json:"label,omitempty"`
	Location           string  `json:"location,omitempty"`
	RatedLoadKg        float64 `json:"ratedLoadKg,omitempty"`
	PlannedLoadKg      float64 `json:"plannedLoadKg,omitempty"`
	InspectionStandard string  `json:"inspectionStandard,omitempty"`
}
type RemediationResult struct {
	HazardID string              `json:"hazardId"`
	Revision *domain.RiggingItem `json:"revision,omitempty"`
	Version  int64               `json:"version"`
}

func (s *Service) Remediate(ctx context.Context, sessionID, hazardID string, c RemediateCommand) (RemediationResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return RemediationResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleTechnician, domain.RoleSupervisor); err != nil {
		return RemediationResult{}, err
	}
	if err := domain.ValidateRemediation(c.Note, c.EvidenceRef, c.Actor); err != nil {
		return RemediationResult{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(snap.Session); err != nil {
			return storage.CommandResult{}, err
		}
		h, err := u.Hazard(hazardID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if h.SessionID != sessionID || h.Status != domain.HazardOpen {
			return storage.CommandResult{}, domain.ErrInvalidState
		}
		result := RemediationResult{HazardID: hazardID, Version: c.ExpectedVersion + 1}
		revisionID := h.RevisionItemID
		currentItemID := h.ItemID
		if revisionID != "" {
			currentItemID = revisionID
		}
		if c.ReviseItem {
			var old domain.RiggingItem
			found := false
			for _, i := range snap.Items {
				if i.ID == currentItemID && i.Active {
					old = i
					found = true
				}
			}
			if !found {
				return storage.CommandResult{}, domain.ErrNotFound
			}
			revised := old
			revised.ID = uuid.NewString()
			revised.SupersedesID = old.ID
			revised.Revision = old.Revision + 1
			if c.Label != "" {
				revised.Label = strings.TrimSpace(c.Label)
			}
			if c.Location != "" {
				revised.Location = strings.TrimSpace(c.Location)
			}
			if c.RatedLoadKg > 0 {
				revised.RatedLoadKg = c.RatedLoadKg
			}
			if c.PlannedLoadKg > 0 {
				revised.PlannedLoadKg = c.PlannedLoadKg
			}
			if c.InspectionStandard != "" {
				revised.InspectionStandard = strings.TrimSpace(c.InspectionStandard)
			}
			if err = domain.ValidateItem(revised); err != nil {
				return storage.CommandResult{}, err
			}
			prospective := make([]domain.RiggingItem, 0, len(snap.Items)+1)
			for _, i := range snap.Items {
				if i.ID == old.ID {
					i.Active = false
				}
				if i.Active && i.ParentItemID == old.ID {
					i.ParentItemID = revised.ID
				}
				prospective = append(prospective, i)
			}
			prospective = append(prospective, revised)
			if err = domain.ValidateGroupLoads(prospective); err != nil {
				return storage.CommandResult{}, err
			}
			if err = u.ReplaceItem(old.ID, revised); err != nil {
				return storage.CommandResult{}, err
			}
			if err = u.ReparentChildren(sessionID, old.ID, revised.ID); err != nil {
				return storage.CommandResult{}, err
			}
			revisionID = revised.ID
			result.Revision = &revised
		}
		if err = u.RemediateHazard(hazardID, strings.TrimSpace(c.Note), strings.TrimSpace(c.EvidenceRef), revisionID); err != nil {
			return storage.CommandResult{}, err
		}
		round, err := u.NextRemediationRound(hazardID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		record := domain.RemediationRecord{ID: uuid.NewString(), HazardID: hazardID, Round: round, Note: strings.TrimSpace(c.Note), EvidenceRef: strings.TrimSpace(c.EvidenceRef), Actor: c.Actor, ItemID: currentItemID, SubmittedAt: time.Now().UTC()}
		if result.Revision != nil {
			record.ItemID = result.Revision.ID
		}
		if err = u.InsertRemediation(record); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(result)
		return mutation(response, "hazard.remediated", result), nil
	})
	if err != nil {
		return RemediationResult{}, err
	}
	var out RemediationResult
	err = json.Unmarshal(b, &out)
	return out, err
}

type ReinspectCommand struct {
	WriteMeta
	MeasuredValue string         `json:"measuredValue"`
	Verdict       domain.Verdict `json:"verdict"`
	EvidenceRef   string         `json:"evidenceRef"`
	InspectorName string         `json:"inspectorName"`
}

func (s *Service) Reinspect(ctx context.Context, sessionID, hazardID string, c ReinspectCommand) (InspectionResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return InspectionResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleTechnician); err != nil {
		return InspectionResult{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(snap.Session); err != nil {
			return storage.CommandResult{}, err
		}
		h, err := u.Hazard(hazardID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if h.Status != domain.HazardAwaitingReinspection {
			return storage.CommandResult{}, domain.ErrInvalidState
		}
		itemID := h.ItemID
		if h.RevisionItemID != "" {
			itemID = h.RevisionItemID
		}
		var check domain.CheckType
		for _, old := range snap.Inspections {
			if old.ID == h.InspectionID {
				check = old.CheckType
			}
		}
		round := 2
		for _, remediation := range snap.Remediations {
			if remediation.HazardID == hazardID && remediation.Round+1 > round {
				round = remediation.Round + 1
			}
		}
		in := domain.Inspection{ID: uuid.NewString(), SessionID: sessionID, ItemID: itemID, Round: round, CheckType: check, MeasuredValue: strings.TrimSpace(c.MeasuredValue), Verdict: c.Verdict, EvidenceRef: strings.TrimSpace(c.EvidenceRef), InspectorName: strings.TrimSpace(c.InspectorName), RecordedAt: time.Now().UTC()}
		if err = domain.ValidateInspection(in, true); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.InsertInspection(in); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.CompleteReinspection(hazardID, in.ID, in.Verdict == domain.VerdictPass); err != nil {
			return storage.CommandResult{}, err
		}
		result := InspectionResult{Inspection: in, Version: c.ExpectedVersion + 1}
		response, _ := json.Marshal(result)
		event := "hazard.reinspection_failed"
		if in.Verdict == domain.VerdictPass {
			event = "hazard.reinspection_passed"
		}
		return mutation(response, event, result), nil
	})
	if err != nil {
		return InspectionResult{}, err
	}
	var out InspectionResult
	err = json.Unmarshal(b, &out)
	return out, err
}

type FreezeCommand struct {
	WriteMeta
	ReviewNote    string          `json:"reviewNote"`
	ReviewToken   string          `json:"reviewToken"`
	Confirmed     bool            `json:"confirmed"`
	Confirmations map[string]bool `json:"confirmations,omitempty"`
}
type FreezeResult struct {
	ManifestDigest string    `json:"manifestDigest"`
	FrozenAt       time.Time `json:"frozenAt"`
	Version        int64     `json:"version"`
}

func (s *Service) Freeze(ctx context.Context, sessionID string, c FreezeCommand) (FreezeResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return FreezeResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleReviewer); err != nil {
		return FreezeResult{}, err
	}
	if strings.TrimSpace(c.ReviewNote) == "" {
		return FreezeResult{}, fmt.Errorf("%w: 复核意见不能为空", domain.ErrInvalidInput)
	}
	if strings.TrimSpace(c.ReviewToken) == "" || !c.Confirmed {
		return FreezeResult{}, fmt.Errorf("%w: 必须提交复核确认令牌并确认冻结内容", domain.ErrInvalidInput)
	}
	for _, name := range []string{"items", "inspections", "hazards", "loads"} {
		if !c.Confirmations[name] {
			return FreezeResult{}, fmt.Errorf("%w: 复核确认项 %s 未勾选", domain.ErrInvalidInput, name)
		}
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(snap.Session); err != nil {
			return storage.CommandResult{}, err
		}
		ready := domain.EvaluateReadiness(snap)
		if !ready.Ready {
			return storage.CommandResult{}, fmt.Errorf("%w: %s", domain.ErrIncomplete, strings.Join(ready.BlockingReasons, "；"))
		}
		digest, content, err := integrity.ManifestDigest(snap)
		if err != nil {
			return storage.CommandResult{}, err
		}
		confirmations, _ := json.Marshal(c.Confirmations)
		if err = u.ConsumeReviewToken(c.ReviewToken, sessionID, c.ExpectedVersion, digest, string(confirmations), strings.TrimSpace(c.ReviewNote), c.Actor); err != nil {
			return storage.CommandResult{}, err
		}
		if err = u.Freeze(sessionID, digest, content); err != nil {
			return storage.CommandResult{}, err
		}
		result := FreezeResult{ManifestDigest: digest, FrozenAt: time.Now().UTC(), Version: c.ExpectedVersion + 1}
		response, _ := json.Marshal(result)
		return mutation(response, "manifest.frozen", map[string]any{"result": result, "reviewNote": c.ReviewNote}), nil
	})
	if err != nil {
		return FreezeResult{}, err
	}
	var out FreezeResult
	err = json.Unmarshal(b, &out)
	return out, err
}

type IssueCommand struct {
	WriteMeta
	ApprovedBy string `json:"approvedBy"`
}

func (s *Service) Issue(ctx context.Context, sessionID string, c IssueCommand) (domain.Certificate, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.Certificate{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleReviewer); err != nil {
		return domain.Certificate{}, err
	}
	if strings.TrimSpace(c.ApprovedBy) == "" {
		return domain.Certificate{}, fmt.Errorf("%w: 批准人不能为空", domain.ErrInvalidInput)
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		session, err := u.Session(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if session.Status != domain.StatusFrozen {
			return storage.CommandResult{}, domain.ErrInvalidState
		}
		sequence, previous, err := u.NextCertificate()
		if err != nil {
			return storage.CommandResult{}, err
		}
		cert := domain.Certificate{ID: uuid.NewString(), SessionID: sessionID, Sequence: sequence, ManifestDigest: session.FrozenDigest, PreviousDigest: previous, ApprovedBy: strings.TrimSpace(c.ApprovedBy), IssuedAt: time.Now().UTC()}
		cert = integrity.SignCertificate(cert)
		if err = u.InsertCertificate(cert); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(cert)
		return mutation(response, "certificate.issued", cert), nil
	})
	if err != nil {
		return domain.Certificate{}, err
	}
	var out domain.Certificate
	err = json.Unmarshal(b, &out)
	return out, err
}

func commandMeta(sessionID string, m WriteMeta, request any) storage.CommandMeta {
	return storage.CommandMeta{IdempotencyKey: m.IdempotencyKey, RequestDigest: storage.DigestRequest(request), SessionID: sessionID, ExpectedVersion: m.ExpectedVersion, Actor: m.Actor}
}
func mutation(response []byte, event string, payload any) storage.CommandResult {
	return storage.CommandResult{Response: response, EventType: event, EventPayload: payload, BumpVersion: true}
}
