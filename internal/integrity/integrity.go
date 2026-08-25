package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"stage-rigging-clearance/internal/domain"
)

type canonicalSession struct {
	ID             string `json:"id"`
	ProductionName string `json:"productionName"`
	Venue          string `json:"venue"`
	ScheduledAt    string `json:"scheduledAt"`
	SupervisorName string `json:"supervisorName"`
}

type canonicalItem struct {
	ID                 string          `json:"id"`
	ParentItemID       string          `json:"parentItemId,omitempty"`
	ItemType           domain.ItemType `json:"itemType"`
	Label              string          `json:"label"`
	Location           string          `json:"location"`
	RatedLoadKg        float64         `json:"ratedLoadKg"`
	PlannedLoadKg      float64         `json:"plannedLoadKg"`
	InspectionStandard string          `json:"inspectionStandard"`
	Revision           int             `json:"revision"`
	SupersedesID       string          `json:"supersedesId,omitempty"`
	SourceSessionID    string          `json:"sourceSessionId,omitempty"`
	SourceItemID       string          `json:"sourceItemId,omitempty"`
	Active             bool            `json:"active"`
}

type canonicalInspection struct {
	ID            string           `json:"id"`
	ItemID        string           `json:"itemId"`
	Round         int              `json:"round"`
	CheckType     domain.CheckType `json:"checkType"`
	MeasuredValue string           `json:"measuredValue"`
	Verdict       domain.Verdict   `json:"verdict"`
	EvidenceRef   string           `json:"evidenceRef"`
	InspectorName string           `json:"inspectorName"`
	RecordedAt    string           `json:"recordedAt"`
}

type canonicalHazard struct {
	ID                  string              `json:"id"`
	InspectionID        string              `json:"inspectionId"`
	ItemID              string              `json:"itemId"`
	Severity            domain.Severity     `json:"severity"`
	Scope               string              `json:"scope"`
	RequiredAction      string              `json:"requiredAction"`
	Assignee            string              `json:"assignee"`
	DueAt               string              `json:"dueAt,omitempty"`
	Status              domain.HazardStatus `json:"status"`
	RemediationNote     string              `json:"remediationNote,omitempty"`
	RemediationEvidence string              `json:"remediationEvidence,omitempty"`
	RevisionItemID      string              `json:"revisionItemId,omitempty"`
	ReinspectionID      string              `json:"reinspectionId,omitempty"`
	ClosedAt            string              `json:"closedAt,omitempty"`
}

type canonicalRemediation struct {
	ID             string `json:"id"`
	HazardID       string `json:"hazardId"`
	Round          int    `json:"round"`
	Note           string `json:"note"`
	EvidenceRef    string `json:"evidenceRef"`
	Actor          string `json:"actor"`
	ItemID         string `json:"itemId,omitempty"`
	SubmittedAt    string `json:"submittedAt"`
	ReinspectionID string `json:"reinspectionId,omitempty"`
}

type canonicalAssignment struct {
	ID          int64  `json:"id"`
	HazardID    string `json:"hazardId"`
	OldAssignee string `json:"oldAssignee"`
	NewAssignee string `json:"newAssignee"`
	OldDueAt    string `json:"oldDueAt"`
	NewDueAt    string `json:"newDueAt"`
	Actor       string `json:"actor"`
	Reason      string `json:"reason"`
	ChangedAt   string `json:"changedAt"`
}

type manifest struct {
	Session      canonicalSession       `json:"session"`
	Items        []canonicalItem        `json:"items"`
	ItemHistory  []canonicalItem        `json:"itemHistory"`
	Inspections  []canonicalInspection  `json:"inspections"`
	Hazards      []canonicalHazard      `json:"hazards"`
	Remediations []canonicalRemediation `json:"remediations"`
	Assignments  []canonicalAssignment  `json:"assignments"`
}

func ManifestBytes(s domain.Snapshot) ([]byte, error) {
	if err := domain.ValidateSnapshot(s); err != nil {
		return nil, err
	}
	m := manifest{Session: canonicalSession{ID: s.Session.ID, ProductionName: s.Session.ProductionName, Venue: s.Session.Venue, ScheduledAt: s.Session.ScheduledAt.UTC().Format(time.RFC3339Nano), SupervisorName: s.Session.SupervisorName}}
	for _, i := range s.Items {
		item := canonicalItem{ID: i.ID, ParentItemID: i.ParentItemID, ItemType: i.ItemType, Label: i.Label, Location: i.Location, RatedLoadKg: i.RatedLoadKg, PlannedLoadKg: i.PlannedLoadKg, InspectionStandard: i.InspectionStandard, Revision: i.Revision, SupersedesID: i.SupersedesID, SourceSessionID: i.SourceSessionID, SourceItemID: i.SourceItemID, Active: i.Active}
		if i.Active {
			m.Items = append(m.Items, item)
		} else {
			m.ItemHistory = append(m.ItemHistory, item)
		}
	}
	for _, i := range s.Inspections {
		m.Inspections = append(m.Inspections, canonicalInspection{ID: i.ID, ItemID: i.ItemID, Round: i.Round, CheckType: i.CheckType, MeasuredValue: i.MeasuredValue, Verdict: i.Verdict, EvidenceRef: i.EvidenceRef, InspectorName: i.InspectorName, RecordedAt: i.RecordedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, h := range s.Hazards {
		dueAt := ""
		if !h.DueAt.IsZero() {
			dueAt = h.DueAt.UTC().Format(time.RFC3339Nano)
		}
		closedAt := ""
		if h.ClosedAt != nil {
			closedAt = h.ClosedAt.UTC().Format(time.RFC3339Nano)
		}
		m.Hazards = append(m.Hazards, canonicalHazard{ID: h.ID, InspectionID: h.InspectionID, ItemID: h.ItemID, Severity: h.Severity, Scope: h.Scope, RequiredAction: h.RequiredAction, Assignee: h.Assignee, DueAt: dueAt, Status: h.Status, RemediationNote: h.RemediationNote, RemediationEvidence: h.RemediationEvidence, RevisionItemID: h.RevisionItemID, ReinspectionID: h.ReinspectionID, ClosedAt: closedAt})
	}
	for _, r := range s.Remediations {
		m.Remediations = append(m.Remediations, canonicalRemediation{ID: r.ID, HazardID: r.HazardID, Round: r.Round, Note: r.Note, EvidenceRef: r.EvidenceRef, Actor: r.Actor, ItemID: r.ItemID, SubmittedAt: r.SubmittedAt.UTC().Format(time.RFC3339Nano), ReinspectionID: r.ReinspectionID})
	}
	for _, a := range s.Assignments {
		m.Assignments = append(m.Assignments, canonicalAssignment{ID: a.ID, HazardID: a.HazardID, OldAssignee: a.OldAssignee, NewAssignee: a.NewAssignee, OldDueAt: a.OldDueAt.UTC().Format(time.RFC3339Nano), NewDueAt: a.NewDueAt.UTC().Format(time.RFC3339Nano), Actor: a.Actor, Reason: a.Reason, ChangedAt: a.ChangedAt.UTC().Format(time.RFC3339Nano)})
	}
	sort.Slice(m.Items, func(i, j int) bool { return m.Items[i].ID < m.Items[j].ID })
	sort.Slice(m.ItemHistory, func(i, j int) bool { return m.ItemHistory[i].ID < m.ItemHistory[j].ID })
	sort.Slice(m.Inspections, func(i, j int) bool { return m.Inspections[i].ID < m.Inspections[j].ID })
	sort.Slice(m.Hazards, func(i, j int) bool { return m.Hazards[i].ID < m.Hazards[j].ID })
	sort.Slice(m.Remediations, func(i, j int) bool { return m.Remediations[i].ID < m.Remediations[j].ID })
	sort.Slice(m.Assignments, func(i, j int) bool { return m.Assignments[i].ID < m.Assignments[j].ID })
	return json.Marshal(m)
}

func ManifestDigest(s domain.Snapshot) (string, []byte, error) {
	b, err := ManifestBytes(s)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), b, nil
}
