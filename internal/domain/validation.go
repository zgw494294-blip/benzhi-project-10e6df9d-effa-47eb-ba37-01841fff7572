package domain

import (
	"fmt"
	"strings"
	"time"
)

func ValidateSession(s Session) error {
	if strings.TrimSpace(s.ProductionName) == "" || strings.TrimSpace(s.Venue) == "" || strings.TrimSpace(s.SupervisorName) == "" || s.ScheduledAt.IsZero() {
		return fmt.Errorf("%w: 演出名称、场地、开演时间和负责人均为必填", ErrInvalidInput)
	}
	return nil
}

func ValidateItem(i RiggingItem) error {
	switch i.ItemType {
	case ItemPoint, ItemBar, ItemCable, ItemConnector:
	default:
		return fmt.Errorf("%w: 未知构件类型", ErrInvalidInput)
	}
	if strings.TrimSpace(i.Label) == "" || strings.TrimSpace(i.Location) == "" || strings.TrimSpace(i.InspectionStandard) == "" {
		return fmt.Errorf("%w: 构件名称、位置和检验标准均为必填", ErrInvalidInput)
	}
	if i.RatedLoadKg <= 0 || i.PlannedLoadKg < 0 {
		return fmt.Errorf("%w: 额定载荷必须大于零且计划载荷不能为负", ErrInvalidInput)
	}
	if i.PlannedLoadKg > i.RatedLoadKg {
		return fmt.Errorf("%w: 构件 %s 计划载荷 %.2fkg 超过额定载荷 %.2fkg", ErrIncomplete, i.Label, i.PlannedLoadKg, i.RatedLoadKg)
	}
	return nil
}

func ValidateHazardDeadline(assignee string, dueAt, scheduledAt, now time.Time) error {
	if strings.TrimSpace(assignee) == "" {
		return fmt.Errorf("%w: 隐患责任人不能为空", ErrInvalidInput)
	}
	if dueAt.IsZero() {
		return fmt.Errorf("%w: 整改截止时间不能为空", ErrInvalidInput)
	}
	if !dueAt.After(now) {
		return fmt.Errorf("%w: 整改截止时间必须晚于当前时间", ErrInvalidInput)
	}
	if !dueAt.Before(scheduledAt) {
		return fmt.Errorf("%w: 整改截止时间必须早于计划开演时间", ErrInvalidInput)
	}
	return nil
}

func ValidateInspection(in Inspection, reinspection bool) error {
	validCheck := false
	for _, c := range RequiredChecks {
		if c == in.CheckType {
			validCheck = true
		}
	}
	if !validCheck || (in.Verdict != VerdictPass && in.Verdict != VerdictFail) || strings.TrimSpace(in.InspectorName) == "" {
		return fmt.Errorf("%w: 检验类型、结论或检验人无效", ErrInvalidInput)
	}
	critical := in.CheckType == CheckLock || in.CheckType == CheckLoad || reinspection
	if critical && strings.TrimSpace(in.EvidenceRef) == "" {
		return fmt.Errorf("%w: 锁止、载荷及复检项目必须附证据引用", ErrIncomplete)
	}
	return nil
}

func ValidateRemediation(note, evidence, assignee string) error {
	if strings.TrimSpace(note) == "" || strings.TrimSpace(evidence) == "" || strings.TrimSpace(assignee) == "" {
		return fmt.Errorf("%w: 整改说明、证据和责任人均为必填", ErrInvalidInput)
	}
	return nil
}

func RequireMutable(s Session) error {
	if s.Status == StatusFrozen || s.Status == StatusReleased {
		return ErrFrozen
	}
	return nil
}

func RequireRole(actual Role, allowed ...Role) error {
	for _, r := range allowed {
		if actual == r {
			return nil
		}
	}
	return ErrForbidden
}
