package domain

import (
	"errors"
	"testing"
	"time"
)

func validSession() Session {
	return Session{ID: "s1", ProductionName: "测试演出", Venue: "测试剧场", ScheduledAt: time.Now().Add(time.Hour), SupervisorName: "舞监", Status: StatusDraft, Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
}

func TestValidateGroupLoads(t *testing.T) {
	items := []RiggingItem{
		{ID: "parent", ItemType: ItemBar, Label: "吊杆", Location: "中区", RatedLoadKg: 500, PlannedLoadKg: 300, InspectionStandard: "S1", Active: true},
		{ID: "child-a", ParentItemID: "parent", ItemType: ItemCable, Label: "钢索 A", Location: "左", RatedLoadKg: 400, PlannedLoadKg: 280, InspectionStandard: "S1", Active: true},
		{ID: "child-b", ParentItemID: "parent", ItemType: ItemCable, Label: "钢索 B", Location: "右", RatedLoadKg: 400, PlannedLoadKg: 250, InspectionStandard: "S1", Active: true},
	}
	if err := ValidateGroupLoads(items); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("应拒绝子项合计超载，实际错误：%v", err)
	}
	items[2].PlannedLoadKg = 200
	if err := ValidateGroupLoads(items); err != nil {
		t.Fatalf("安全载荷应通过：%v", err)
	}
}

func TestCriticalInspectionRequiresEvidence(t *testing.T) {
	in := Inspection{CheckType: CheckLock, Verdict: VerdictPass, InspectorName: "技师"}
	if err := ValidateInspection(in, false); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("锁止检查缺证据应失败：%v", err)
	}
	in.EvidenceRef = "evidence://lock"
	if err := ValidateInspection(in, false); err != nil {
		t.Fatalf("附证据后应通过：%v", err)
	}
}

func TestEvaluateReadinessWithClosedHazard(t *testing.T) {
	s := Snapshot{Session: validSession(), Items: []RiggingItem{{ID: "i1", ItemType: ItemPoint, Label: "吊点", Location: "左", RatedLoadKg: 100, PlannedLoadKg: 50, InspectionStandard: "S1", Active: true}}}
	for n, check := range RequiredChecks {
		s.Inspections = append(s.Inspections, Inspection{ID: string(rune('a' + n)), ItemID: "i1", Round: 1, CheckType: check, Verdict: VerdictPass, RecordedAt: time.Now()})
	}
	r := EvaluateReadiness(s)
	if !r.Ready || r.Coverage != 1 {
		t.Fatalf("完整检验应可冻结：%+v", r)
	}
	s.Hazards = append(s.Hazards, Hazard{ID: "h1", Severity: SeverityBlocking, Status: HazardOpen})
	if EvaluateReadiness(s).Ready {
		t.Fatal("开放阻断隐患必须阻止冻结")
	}
}
