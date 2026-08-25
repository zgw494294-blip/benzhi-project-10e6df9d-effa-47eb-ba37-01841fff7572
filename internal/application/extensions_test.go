package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stage-rigging-clearance/internal/domain"
	"stage-rigging-clearance/internal/storage"
)

func testService(t *testing.T) *Service {
	t.Helper()
	store, err := storage.Open("file:" + t.Name() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return New(store)
}

func testMeta(version int64, key string, role domain.Role) WriteMeta {
	return WriteMeta{ExpectedVersion: version, IdempotencyKey: key + "-12345678", Actor: "测试操作者", Role: role}
}

func createTestSession(t *testing.T, service *Service, key string) domain.Session {
	t.Helper()
	session, err := service.CreateSession(context.Background(), CreateSessionCommand{WriteMeta: testMeta(0, key, domain.RoleSupervisor), ProductionName: "测试演出", Venue: "测试剧场", ScheduledAt: time.Now().Add(24 * time.Hour), SupervisorName: "舞台监督"})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestBulkItemsPreflightAndCommitAreAtomic(t *testing.T) {
	service := testService(t)
	session := createTestSession(t, service, "bulk-create")
	rows := []BulkItemRow{
		{TempID: "P", ItemType: domain.ItemBar, Label: "吊杆", Location: "中区", RatedLoadKg: 500, PlannedLoadKg: 400, InspectionStandard: "S1"},
		{TempID: "C1", ParentRef: "P", ItemType: domain.ItemCable, Label: "钢索一", Location: "左区", RatedLoadKg: 400, PlannedLoadKg: 300, InspectionStandard: "S1"},
		{TempID: "C2", ParentRef: "P", ItemType: domain.ItemCable, Label: "钢索二", Location: "右区", RatedLoadKg: 400, PlannedLoadKg: 250, InspectionStandard: "S1"},
	}
	preflight, err := service.PreflightBulkItems(context.Background(), session.ID, BulkItemPreflightCommand{ExpectedVersion: 1, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Valid || len(preflight.Rows[1].Errors) == 0 || len(preflight.Rows[2].Errors) == 0 {
		t.Fatalf("父级超载应定位到相关构件行：%+v", preflight)
	}
	_, err = service.ImportItems(context.Background(), session.ID, BulkImportItemsCommand{WriteMeta: testMeta(1, "bulk-invalid", domain.RoleSupervisor), Rows: rows})
	if !errors.Is(err, domain.ErrIncomplete) {
		t.Fatalf("强制提交超载批次应失败：%v", err)
	}
	workbench, err := service.Workbench(context.Background(), session.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Snapshot.Items) != 0 || workbench.Snapshot.Session.Version != 1 {
		t.Fatalf("失败批量命令不得留下部分事实：%+v", workbench.Snapshot)
	}
	rows[2].PlannedLoadKg = 200
	result, err := service.ImportItems(context.Background(), session.ID, BulkImportItemsCommand{WriteMeta: testMeta(1, "bulk-valid", domain.RoleSupervisor), Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 || result.Version != 2 || result.TaskCount != 15 {
		t.Fatalf("合法批量命令结果异常：%+v", result)
	}
}

func TestFailedReinspectionRequiresNewRemediationRound(t *testing.T) {
	service := testService(t)
	session := createTestSession(t, service, "round-create")
	item, err := service.AddItem(context.Background(), session.ID, AddItemCommand{WriteMeta: testMeta(1, "round-item", domain.RoleSupervisor), ItemType: domain.ItemCable, Label: "主钢索", Location: "中区", RatedLoadKg: 500, PlannedLoadKg: 250, InspectionStandard: "S1"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := service.SubmitInspection(context.Background(), session.ID, SubmitInspectionCommand{WriteMeta: testMeta(2, "round-inspection", domain.RoleTechnician), ItemID: item.ID, CheckType: domain.CheckWear, MeasuredValue: "磨损", Verdict: domain.VerdictFail, InspectorName: "技师", Severity: domain.SeverityBlocking, Scope: "钢索", RequiredAction: "整改", Assignee: "责任人", DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Hazard == nil {
		t.Fatal("不合格检验应建立隐患")
	}
	hazardID := initial.Hazard.ID
	if _, err = service.Remediate(context.Background(), session.ID, hazardID, RemediateCommand{WriteMeta: testMeta(3, "round-fix-1", domain.RoleTechnician), Note: "第一次整改", EvidenceRef: "evidence://fix/1"}); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Reinspect(context.Background(), session.ID, hazardID, ReinspectCommand{WriteMeta: testMeta(4, "round-check-1", domain.RoleTechnician), MeasuredValue: "仍有磨损", Verdict: domain.VerdictFail, EvidenceRef: "evidence://check/1", InspectorName: "复检人"})
	if err != nil || failed.Inspection.Round != 2 {
		t.Fatalf("失败复检应保存为第二轮：%+v %v", failed, err)
	}
	_, err = service.Reinspect(context.Background(), session.ID, hazardID, ReinspectCommand{WriteMeta: testMeta(5, "round-check-repeat", domain.RoleTechnician), MeasuredValue: "重复", Verdict: domain.VerdictPass, EvidenceRef: "evidence://repeat", InspectorName: "复检人"})
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("未提交新整改不得再次复检：%v", err)
	}
	if _, err = service.Remediate(context.Background(), session.ID, hazardID, RemediateCommand{WriteMeta: testMeta(5, "round-fix-2", domain.RoleTechnician), Note: "第二次整改", EvidenceRef: "evidence://fix/2"}); err != nil {
		t.Fatal(err)
	}
	passed, err := service.Reinspect(context.Background(), session.ID, hazardID, ReinspectCommand{WriteMeta: testMeta(6, "round-check-2", domain.RoleTechnician), MeasuredValue: "合格", Verdict: domain.VerdictPass, EvidenceRef: "evidence://check/2", InspectorName: "复检人"})
	if err != nil || passed.Inspection.Round != 3 {
		t.Fatalf("第三轮合格复检异常：%+v %v", passed, err)
	}
	workbench, err := service.Workbench(context.Background(), session.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := workbench.Snapshot.Hazards[0].Status; got != domain.HazardClosed {
		t.Fatalf("最新合格复检后隐患应关闭，实际 %s", got)
	}
	if len(workbench.Snapshot.Remediations) != 2 {
		t.Fatalf("应保留两轮整改，实际 %d", len(workbench.Snapshot.Remediations))
	}
	for index, remediation := range workbench.Snapshot.Remediations {
		if remediation.ReinspectionID == "" {
			t.Fatalf("第 %d 轮整改缺少复检关联：%s", index+1, fmt.Sprint(remediation))
		}
	}
}
