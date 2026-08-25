package domain

import (
	"errors"
	"time"
)

type SessionStatus string

const (
	StatusDraft      SessionStatus = "draft"
	StatusInspecting SessionStatus = "inspecting"
	StatusFrozen     SessionStatus = "frozen"
	StatusReleased   SessionStatus = "released"
)

type Role string

const (
	RoleSupervisor Role = "supervisor"
	RoleTechnician Role = "technician"
	RoleReviewer   Role = "reviewer"
)

type ItemType string

const (
	ItemPoint     ItemType = "point"
	ItemBar       ItemType = "bar"
	ItemCable     ItemType = "cable"
	ItemConnector ItemType = "connector"
)

type CheckType string

const (
	CheckLock      CheckType = "lock"
	CheckWear      CheckType = "wear"
	CheckFastening CheckType = "fastening"
	CheckClearance CheckType = "clearance"
	CheckLoad      CheckType = "load"
)

var RequiredChecks = []CheckType{CheckLock, CheckWear, CheckFastening, CheckClearance, CheckLoad}

type Verdict string

const (
	VerdictPending Verdict = "pending"
	VerdictPass    Verdict = "pass"
	VerdictFail    Verdict = "fail"
)

type Severity string

const (
	SeverityAdvisory Severity = "advisory"
	SeverityBlocking Severity = "blocking"
)

type HazardStatus string

const (
	HazardOpen                 HazardStatus = "open"
	HazardAwaitingReinspection HazardStatus = "awaiting_reinspection"
	HazardClosed               HazardStatus = "closed"
)

var (
	ErrNotFound        = errors.New("记录不存在")
	ErrVersionConflict = errors.New("版本冲突")
	ErrFrozen          = errors.New("清单已冻结，事实不可修改")
	ErrInvalidState    = errors.New("当前状态不允许此操作")
	ErrIncomplete      = errors.New("核验尚未满足前置条件")
	ErrIdempotency     = errors.New("幂等键已用于不同请求")
	ErrForbidden       = errors.New("操作者角色无权执行此操作")
	ErrInvalidInput    = errors.New("输入不合法")
)

type Session struct {
	ID             string        `json:"id"`
	ProductionName string        `json:"productionName"`
	Venue          string        `json:"venue"`
	ScheduledAt    time.Time     `json:"scheduledAt"`
	SupervisorName string        `json:"supervisorName"`
	Status         SessionStatus `json:"status"`
	Version        int64         `json:"version"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	FrozenDigest   string        `json:"frozenDigest,omitempty"`
}

type RiggingItem struct {
	ID                 string   `json:"id"`
	SessionID          string   `json:"sessionId"`
	ParentItemID       string   `json:"parentItemId,omitempty"`
	ItemType           ItemType `json:"itemType"`
	Label              string   `json:"label"`
	Location           string   `json:"location"`
	RatedLoadKg        float64  `json:"ratedLoadKg"`
	PlannedLoadKg      float64  `json:"plannedLoadKg"`
	InspectionStandard string   `json:"inspectionStandard"`
	Revision           int      `json:"revision"`
	SupersedesID       string   `json:"supersedesId,omitempty"`
	SourceSessionID    string   `json:"sourceSessionId,omitempty"`
	SourceItemID       string   `json:"sourceItemId,omitempty"`
	Active             bool     `json:"active"`
}

func (i RiggingItem) MarginKg() float64 { return i.RatedLoadKg - i.PlannedLoadKg }
func (i RiggingItem) Utilization() float64 {
	if i.RatedLoadKg == 0 {
		return 0
	}
	return i.PlannedLoadKg / i.RatedLoadKg
}

type Inspection struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"sessionId"`
	ItemID        string    `json:"itemId"`
	Round         int       `json:"round"`
	CheckType     CheckType `json:"checkType"`
	MeasuredValue string    `json:"measuredValue"`
	Verdict       Verdict   `json:"verdict"`
	EvidenceRef   string    `json:"evidenceRef"`
	InspectorName string    `json:"inspectorName"`
	RecordedAt    time.Time `json:"recordedAt"`
}

type Hazard struct {
	ID                  string       `json:"id"`
	SessionID           string       `json:"sessionId"`
	InspectionID        string       `json:"inspectionId"`
	ItemID              string       `json:"itemId"`
	Severity            Severity     `json:"severity"`
	Scope               string       `json:"scope"`
	RequiredAction      string       `json:"requiredAction"`
	Assignee            string       `json:"assignee"`
	DueAt               time.Time    `json:"dueAt"`
	Status              HazardStatus `json:"status"`
	RemediationNote     string       `json:"remediationNote,omitempty"`
	RemediationEvidence string       `json:"remediationEvidence,omitempty"`
	RevisionItemID      string       `json:"revisionItemId,omitempty"`
	ReinspectionID      string       `json:"reinspectionId,omitempty"`
	ClosedAt            *time.Time   `json:"closedAt,omitempty"`
}

type HazardAssignment struct {
	ID          int64     `json:"id"`
	HazardID    string    `json:"hazardId"`
	OldAssignee string    `json:"oldAssignee"`
	NewAssignee string    `json:"newAssignee"`
	OldDueAt    time.Time `json:"oldDueAt"`
	NewDueAt    time.Time `json:"newDueAt"`
	Actor       string    `json:"actor"`
	Reason      string    `json:"reason"`
	ChangedAt   time.Time `json:"changedAt"`
}

type RemediationRecord struct {
	ID             string    `json:"id"`
	HazardID       string    `json:"hazardId"`
	Round          int       `json:"round"`
	Note           string    `json:"note"`
	EvidenceRef    string    `json:"evidenceRef"`
	Actor          string    `json:"actor"`
	ItemID         string    `json:"itemId,omitempty"`
	SubmittedAt    time.Time `json:"submittedAt"`
	ReinspectionID string    `json:"reinspectionId,omitempty"`
}

type Certificate struct {
	ID                string    `json:"id"`
	SessionID         string    `json:"sessionId"`
	Sequence          int64     `json:"sequence"`
	ManifestDigest    string    `json:"manifestDigest"`
	PreviousDigest    string    `json:"previousDigest"`
	CertificateDigest string    `json:"certificateDigest"`
	ApprovedBy        string    `json:"approvedBy"`
	IssuedAt          time.Time `json:"issuedAt"`
}

type AuditEvent struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"sessionId"`
	EventType string    `json:"eventType"`
	Actor     string    `json:"actor"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"createdAt"`
}

type Snapshot struct {
	Session      Session             `json:"session"`
	Items        []RiggingItem       `json:"items"`
	Inspections  []Inspection        `json:"inspections"`
	Hazards      []Hazard            `json:"hazards"`
	Remediations []RemediationRecord `json:"remediations"`
	Assignments  []HazardAssignment  `json:"assignments"`
	Certificates []Certificate       `json:"certificates"`
}

type LoadSummary struct {
	ItemCount       int     `json:"itemCount"`
	RatedTotalKg    float64 `json:"ratedTotalKg"`
	PlannedTotalKg  float64 `json:"plannedTotalKg"`
	MinimumMarginKg float64 `json:"minimumMarginKg"`
	Overloaded      bool    `json:"overloaded"`
}
