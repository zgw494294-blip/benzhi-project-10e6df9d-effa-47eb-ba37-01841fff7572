package application

import (
	"context"
	"time"

	"stage-rigging-clearance/internal/domain"
	"stage-rigging-clearance/internal/integrity"
)

type InspectionTask struct {
	ItemID       string           `json:"itemId"`
	ItemLabel    string           `json:"itemLabel"`
	Location     string           `json:"location"`
	ItemType     domain.ItemType  `json:"itemType"`
	CheckType    domain.CheckType `json:"checkType"`
	Round        int              `json:"round"`
	Status       string           `json:"status"`
	InspectionID string           `json:"inspectionId,omitempty"`
}

type HazardDeadlineSummary struct {
	Open    int `json:"open"`
	DueSoon int `json:"dueSoon"`
	Overdue int `json:"overdue"`
}

type Workbench struct {
	Snapshot        domain.Snapshot              `json:"snapshot"`
	Load            domain.LoadSummary           `json:"load"`
	Readiness       domain.Readiness             `json:"readiness"`
	Tasks           []InspectionTask             `json:"tasks"`
	Verification    integrity.VerificationResult `json:"verification"`
	Audit           []domain.AuditEvent          `json:"audit"`
	HazardDeadlines HazardDeadlineSummary        `json:"hazardDeadlines"`
}

func (s *Service) Workbench(ctx context.Context, sessionID string, auditLimit, auditOffset int) (Workbench, error) {
	snap, err := s.store.Snapshot(ctx, sessionID)
	if err != nil {
		return Workbench{}, err
	}
	audit, err := s.store.Audit(ctx, sessionID, auditLimit, auditOffset)
	if err != nil {
		return Workbench{}, err
	}
	verify := integrity.VerificationResult{Valid: false, Errors: []string{"清单尚未冻结"}}
	if snap.Session.FrozenDigest != "" {
		ledger, ledgerErr := s.store.CertificateChain(ctx)
		if ledgerErr != nil {
			return Workbench{}, ledgerErr
		}
		verify = integrity.VerifyLedger(ledger)
		if len(snap.Certificates) == 0 {
			verify.Valid = false
			verify.Errors = append(verify.Errors, "当前批次尚未签发凭据")
		}
		for _, certificate := range snap.Certificates {
			if certificate.ManifestDigest != snap.Session.FrozenDigest {
				verify.Valid = false
				verify.Errors = append(verify.Errors, "当前批次凭据与冻结摘要不一致")
			}
		}
		if digest, _, e := integrity.ManifestDigest(snap); e != nil || digest != snap.Session.FrozenDigest {
			verify.Valid = false
			verify.Errors = append(verify.Errors, "当前事实与冻结摘要不一致")
		}
	}
	return Workbench{Snapshot: snap, Load: domain.CalculateLoadSummary(snap.Items), Readiness: domain.EvaluateReadiness(snap), Tasks: buildTasks(snap), Verification: verify, Audit: audit, HazardDeadlines: deadlineSummary(snap, time.Now().UTC())}, nil
}

func deadlineSummary(snap domain.Snapshot, now time.Time) HazardDeadlineSummary {
	var out HazardDeadlineSummary
	for _, hazard := range snap.Hazards {
		if hazard.Status == domain.HazardClosed {
			continue
		}
		out.Open++
		if !hazard.DueAt.IsZero() && now.After(hazard.DueAt) {
			out.Overdue++
		} else if !hazard.DueAt.IsZero() && hazard.DueAt.Sub(now) <= 24*time.Hour {
			out.DueSoon++
		}
	}
	return out
}

func buildTasks(snap domain.Snapshot) []InspectionTask {
	latest := map[string]domain.Inspection{}
	lineage := map[string]string{}
	byID := map[string]domain.RiggingItem{}
	for _, item := range snap.Items {
		byID[item.ID] = item
	}
	for _, item := range snap.Items {
		if !item.Active {
			continue
		}
		for current := item; current.ID != ""; {
			lineage[current.ID] = item.ID
			if current.SupersedesID == "" {
				break
			}
			current = byID[current.SupersedesID]
		}
	}
	for _, in := range snap.Inspections {
		if in.Round == 1 {
			if activeID := lineage[in.ItemID]; activeID != "" {
				latest[activeID+":"+string(in.CheckType)] = in
			}
		}
	}
	var out []InspectionTask
	for _, item := range snap.Items {
		if !item.Active {
			continue
		}
		for _, check := range domain.RequiredChecks {
			task := InspectionTask{ItemID: item.ID, ItemLabel: item.Label, Location: item.Location, ItemType: item.ItemType, CheckType: check, Round: 1, Status: "pending"}
			if in, ok := latest[item.ID+":"+string(check)]; ok {
				task.Status = string(in.Verdict)
				task.InspectionID = in.ID
			}
			out = append(out, task)
		}
	}
	return out
}

func (s *Service) ListSessions(ctx context.Context) ([]domain.Session, error) {
	return s.store.ListSessions(ctx)
}

func (s *Service) Audit(ctx context.Context, id string, limit, offset int) ([]domain.AuditEvent, error) {
	return s.store.Audit(ctx, id, limit, offset)
}
