package domain

import (
	"fmt"
	"sort"
	"time"
)

type Readiness struct {
	Ready           bool     `json:"ready"`
	Coverage        float64  `json:"coverage"`
	Completed       int      `json:"completed"`
	Required        int      `json:"required"`
	OpenBlocking    int      `json:"openBlocking"`
	OpenAdvisory    int      `json:"openAdvisory"`
	Overdue         int      `json:"overdue"`
	BlockingReasons []string `json:"blockingReasons"`
}

func EvaluateReadiness(snapshot Snapshot) Readiness {
	r := Readiness{}
	active := map[string]bool{}
	lineage := map[string]string{}
	byID := map[string]RiggingItem{}
	for _, i := range snapshot.Items {
		byID[i.ID] = i
	}
	for _, i := range snapshot.Items {
		if i.Active {
			active[i.ID] = true
			r.Required += len(RequiredChecks)
			for current := i; current.ID != ""; {
				lineage[current.ID] = i.ID
				if current.SupersedesID == "" {
					break
				}
				current = byID[current.SupersedesID]
			}
		}
	}
	latest := map[string]Inspection{}
	for _, in := range snapshot.Inspections {
		activeID := lineage[in.ItemID]
		if !active[activeID] || in.Round != 1 {
			continue
		}
		key := activeID + ":" + string(in.CheckType)
		if prior, ok := latest[key]; !ok || in.RecordedAt.After(prior.RecordedAt) {
			latest[key] = in
		}
	}
	for _, in := range latest {
		if in.Verdict == VerdictPass || in.Verdict == VerdictFail {
			r.Completed++
		}
	}
	if r.Required > 0 {
		r.Coverage = float64(r.Completed) / float64(r.Required)
	}
	for _, h := range snapshot.Hazards {
		if h.Status == HazardClosed {
			continue
		}
		if h.Severity == SeverityBlocking {
			r.OpenBlocking++
			if !h.DueAt.IsZero() && time.Now().UTC().After(h.DueAt) {
				r.BlockingReasons = append(r.BlockingReasons, fmt.Sprintf("阻断级隐患已逾期：责任人 %s，逾期 %s", h.Assignee, formatDuration(time.Since(h.DueAt))))
			}
		} else {
			r.OpenAdvisory++
		}
		if !h.DueAt.IsZero() && time.Now().UTC().After(h.DueAt) {
			r.Overdue++
		}
	}
	if r.Required == 0 {
		r.BlockingReasons = append(r.BlockingReasons, "尚未登记吊挂构件")
	}
	if r.Completed < r.Required {
		r.BlockingReasons = append(r.BlockingReasons, fmt.Sprintf("检验覆盖不足：%d/%d", r.Completed, r.Required))
	}
	if r.OpenBlocking > 0 {
		r.BlockingReasons = append(r.BlockingReasons, fmt.Sprintf("仍有 %d 项阻断级隐患未关闭", r.OpenBlocking))
	}
	if CalculateLoadSummary(snapshot.Items).Overloaded {
		r.BlockingReasons = append(r.BlockingReasons, "存在超载构件")
	}
	r.Ready = len(r.BlockingReasons) == 0
	return r
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f 小时", d.Hours())
}

func ValidateSnapshot(s Snapshot) error {
	if err := ValidateSession(s.Session); err != nil {
		return err
	}
	if s.Session.Version < 1 {
		return fmt.Errorf("%w: 版本必须为正数", ErrInvalidInput)
	}
	if err := ValidateGroupLoads(s.Items); err != nil {
		return err
	}
	itemIDs := map[string]bool{}
	inspectionIDs := map[string]bool{}
	inspectionByID := map[string]Inspection{}
	hazardIDs := map[string]bool{}
	for _, i := range s.Items {
		itemIDs[i.ID] = true
	}
	for _, in := range s.Inspections {
		if !itemIDs[in.ItemID] {
			return fmt.Errorf("%w: 检验引用未知构件", ErrInvalidInput)
		}
		inspectionIDs[in.ID] = true
		inspectionByID[in.ID] = in
	}
	for _, h := range s.Hazards {
		hazardIDs[h.ID] = true
		if !inspectionIDs[h.InspectionID] {
			return fmt.Errorf("%w: 隐患引用未知检验", ErrInvalidInput)
		}
		if h.Status == HazardClosed && h.ReinspectionID == "" {
			return fmt.Errorf("%w: 已关闭隐患缺少复检记录", ErrInvalidInput)
		}
		if h.Status == HazardClosed {
			reinspection, ok := inspectionByID[h.ReinspectionID]
			if !ok || reinspection.Round < 2 || reinspection.Verdict != VerdictPass {
				return fmt.Errorf("%w: 已关闭隐患的最新复检无效", ErrInvalidInput)
			}
		}
	}
	remediationRounds := map[string]bool{}
	for _, remediation := range s.Remediations {
		if !hazardIDs[remediation.HazardID] || remediation.Round < 1 {
			return fmt.Errorf("%w: 整改记录引用未知隐患或轮次无效", ErrInvalidInput)
		}
		key := fmt.Sprintf("%s:%d", remediation.HazardID, remediation.Round)
		if remediationRounds[key] {
			return fmt.Errorf("%w: 整改轮次重复", ErrInvalidInput)
		}
		remediationRounds[key] = true
		if remediation.ItemID != "" && !itemIDs[remediation.ItemID] {
			return fmt.Errorf("%w: 整改记录引用未知构件", ErrInvalidInput)
		}
		if remediation.ReinspectionID != "" && !inspectionIDs[remediation.ReinspectionID] {
			return fmt.Errorf("%w: 整改记录引用未知复检", ErrInvalidInput)
		}
	}
	for _, assignment := range s.Assignments {
		if !hazardIDs[assignment.HazardID] {
			return fmt.Errorf("%w: 转派记录引用未知隐患", ErrInvalidInput)
		}
	}
	if (s.Session.Status == StatusFrozen || s.Session.Status == StatusReleased) && !EvaluateReadiness(s).Ready {
		return fmt.Errorf("%w: 冻结快照不满足安全条件", ErrInvalidState)
	}
	return nil
}

func SortSnapshot(s *Snapshot) {
	sort.Slice(s.Items, func(i, j int) bool { return s.Items[i].ID < s.Items[j].ID })
	sort.Slice(s.Inspections, func(i, j int) bool { return s.Inspections[i].ID < s.Inspections[j].ID })
	sort.Slice(s.Hazards, func(i, j int) bool { return s.Hazards[i].ID < s.Hazards[j].ID })
	sort.Slice(s.Certificates, func(i, j int) bool { return s.Certificates[i].Sequence < s.Certificates[j].Sequence })
}
