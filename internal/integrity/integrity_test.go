package integrity

import (
	"testing"
	"time"

	"stage-rigging-clearance/internal/domain"
)

func manifestSnapshot() domain.Snapshot {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	s := domain.Snapshot{Session: domain.Session{ID: "s", ProductionName: "演出", Venue: "剧场", ScheduledAt: now, SupervisorName: "舞监", Status: domain.StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}, Items: []domain.RiggingItem{{ID: "i", SessionID: "s", ItemType: domain.ItemBar, Label: "吊杆", Location: "中区", RatedLoadKg: 500, PlannedLoadKg: 200, InspectionStandard: "标准", Revision: 1, Active: true}}}
	for index, check := range domain.RequiredChecks {
		s.Inspections = append(s.Inspections, domain.Inspection{ID: string(rune('a' + index)), SessionID: "s", ItemID: "i", Round: 1, CheckType: check, Verdict: domain.VerdictPass, InspectorName: "技师", EvidenceRef: "evidence://x", RecordedAt: now})
	}
	return s
}

func TestManifestDigestStableAcrossSliceOrder(t *testing.T) {
	a := manifestSnapshot()
	b := manifestSnapshot()
	b.Inspections[0], b.Inspections[4] = b.Inspections[4], b.Inspections[0]
	da, _, err := ManifestDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	db, _, err := ManifestDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatalf("相同事实摘要不稳定：%s != %s", da, db)
	}
}

func TestVerifyChainDetectsTampering(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	first := SignCertificate(domain.Certificate{ID: "c1", SessionID: "s", Sequence: 1, ManifestDigest: "manifest", ApprovedBy: "复核", IssuedAt: now})
	second := SignCertificate(domain.Certificate{ID: "c2", SessionID: "s", Sequence: 2, ManifestDigest: "manifest", PreviousDigest: first.CertificateDigest, ApprovedBy: "复核", IssuedAt: now.Add(time.Minute)})
	if result := VerifyChain([]domain.Certificate{second, first}, "manifest"); !result.Valid {
		t.Fatalf("有效乱序输入应按序验证：%+v", result)
	}
	second.ApprovedBy = "篡改者"
	if result := VerifyChain([]domain.Certificate{first, second}, "manifest"); result.Valid {
		t.Fatal("内容篡改应被检测")
	}
}

func TestVerifyLedgerAllowsDifferentManifests(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	first := SignCertificate(domain.Certificate{ID: "c1", SessionID: "s1", Sequence: 1, ManifestDigest: "manifest-1", ApprovedBy: "甲", IssuedAt: now})
	second := SignCertificate(domain.Certificate{ID: "c2", SessionID: "s2", Sequence: 2, ManifestDigest: "manifest-2", PreviousDigest: first.CertificateDigest, ApprovedBy: "乙", IssuedAt: now.Add(time.Minute)})
	if result := VerifyLedger([]domain.Certificate{second, first}); !result.Valid {
		t.Fatalf("跨批次全局凭据链应有效：%+v", result)
	}
}
