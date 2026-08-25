package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"stage-rigging-clearance/internal/domain"
	"stage-rigging-clearance/internal/storage"
)

type ReusablePlanSummary struct {
	Session domain.Session       `json:"session"`
	Items   []domain.RiggingItem `json:"items"`
	Load    domain.LoadSummary   `json:"load"`
}

func (s *Service) ReusablePlans(ctx context.Context) ([]ReusablePlanSummary, error) {
	sessions, err := s.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []ReusablePlanSummary
	for _, session := range sessions {
		if session.Status != domain.StatusReleased {
			continue
		}
		snap, err := s.store.Snapshot(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		var active []domain.RiggingItem
		for _, item := range snap.Items {
			if item.Active {
				active = append(active, item)
			}
		}
		out = append(out, ReusablePlanSummary{Session: session, Items: active, Load: domain.CalculateLoadSummary(active)})
	}
	return out, nil
}

type BulkItemRow struct {
	TempID             string          `json:"tempId"`
	ParentRef          string          `json:"parentRef,omitempty"`
	ItemType           domain.ItemType `json:"itemType"`
	Label              string          `json:"label"`
	Location           string          `json:"location"`
	RatedLoadKg        float64         `json:"ratedLoadKg"`
	PlannedLoadKg      float64         `json:"plannedLoadKg"`
	InspectionStandard string          `json:"inspectionStandard"`
}

type BulkItemPreflightCommand struct {
	ExpectedVersion int64         `json:"expectedVersion"`
	Rows            []BulkItemRow `json:"rows"`
}

type RowValidation struct {
	Row    int      `json:"row"`
	TempID string   `json:"tempId"`
	Errors []string `json:"errors"`
}

type BulkItemPreflight struct {
	Valid bool               `json:"valid"`
	Rows  []RowValidation    `json:"rows"`
	Load  domain.LoadSummary `json:"load"`
}

func (s *Service) PreflightBulkItems(ctx context.Context, sessionID string, c BulkItemPreflightCommand) (BulkItemPreflight, error) {
	snap, err := s.store.Snapshot(ctx, sessionID)
	if err != nil {
		return BulkItemPreflight{}, err
	}
	if snap.Session.Version != c.ExpectedVersion {
		return BulkItemPreflight{}, fmt.Errorf("%w: 当前版本为 %d", domain.ErrVersionConflict, snap.Session.Version)
	}
	if snap.Session.Status != domain.StatusDraft {
		return BulkItemPreflight{}, fmt.Errorf("%w: 仅草拟批次可批量导入构件", domain.ErrInvalidState)
	}
	_, result := prepareBulkItems(snap, c.Rows)
	return result, nil
}

type BulkImportItemsCommand struct {
	WriteMeta
	Rows []BulkItemRow `json:"rows"`
}

type BulkImportItemsResult struct {
	Items     []domain.RiggingItem `json:"items"`
	Version   int64                `json:"version"`
	TaskCount int                  `json:"taskCount"`
}

func (s *Service) ImportItems(ctx context.Context, sessionID string, c BulkImportItemsCommand) (BulkImportItemsResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return BulkImportItemsResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return BulkImportItemsResult{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if snap.Session.Status != domain.StatusDraft {
			return storage.CommandResult{}, fmt.Errorf("%w: 仅草拟批次可批量导入构件", domain.ErrInvalidState)
		}
		items, check := prepareBulkItems(snap, c.Rows)
		if !check.Valid {
			return storage.CommandResult{}, fmt.Errorf("%w: %s", domain.ErrIncomplete, joinRowErrors(check.Rows))
		}
		if err = u.InsertItems(items); err != nil {
			return storage.CommandResult{}, err
		}
		result := BulkImportItemsResult{Items: items, Version: c.ExpectedVersion + 1, TaskCount: len(items) * len(domain.RequiredChecks)}
		response, _ := json.Marshal(result)
		return mutation(response, "items.bulk_imported", result), nil
	})
	if err != nil {
		return BulkImportItemsResult{}, err
	}
	var out BulkImportItemsResult
	err = json.Unmarshal(b, &out)
	return out, err
}

func prepareBulkItems(snap domain.Snapshot, rows []BulkItemRow) ([]domain.RiggingItem, BulkItemPreflight) {
	result := BulkItemPreflight{Rows: make([]RowValidation, len(rows))}
	if len(rows) == 0 || len(rows) > 500 {
		if len(rows) == 0 {
			result.Rows = []RowValidation{{Row: 0, Errors: []string{"至少需要一行构件"}}}
		} else {
			result.Rows = []RowValidation{{Row: 0, Errors: []string{"单次最多导入 500 行构件"}}}
		}
		return nil, result
	}
	existingIDs := map[string]bool{}
	labels := map[string]int{}
	for _, item := range snap.Items {
		if item.Active {
			existingIDs[item.ID] = true
			labels[strings.ToLower(item.Label)] = -1
		}
	}
	tempRows := map[string]int{}
	ids := map[string]string{}
	for index, row := range rows {
		result.Rows[index] = RowValidation{Row: index + 1, TempID: row.TempID}
		row.TempID = strings.TrimSpace(row.TempID)
		if row.TempID == "" {
			result.Rows[index].Errors = append(result.Rows[index].Errors, "临时编号不能为空")
		} else if prior, ok := tempRows[row.TempID]; ok {
			result.Rows[index].Errors = append(result.Rows[index].Errors, fmt.Sprintf("临时编号与第 %d 行重复", prior+1))
			result.Rows[prior].Errors = append(result.Rows[prior].Errors, fmt.Sprintf("临时编号与第 %d 行重复", index+1))
		} else {
			tempRows[row.TempID] = index
			ids[row.TempID] = uuid.NewString()
		}
		labelKey := strings.ToLower(strings.TrimSpace(row.Label))
		if prior, ok := labels[labelKey]; ok && labelKey != "" {
			if prior < 0 {
				result.Rows[index].Errors = append(result.Rows[index].Errors, "名称与现有活动构件重复")
			} else {
				result.Rows[index].Errors = append(result.Rows[index].Errors, fmt.Sprintf("名称与第 %d 行重复", prior+1))
				result.Rows[prior].Errors = append(result.Rows[prior].Errors, fmt.Sprintf("名称与第 %d 行重复", index+1))
			}
		} else {
			labels[labelKey] = index
		}
	}
	items := make([]domain.RiggingItem, len(rows))
	for index, row := range rows {
		parentID := ""
		if row.ParentRef != "" {
			if generated := ids[row.ParentRef]; generated != "" {
				parentID = generated
			} else if existingIDs[row.ParentRef] {
				parentID = row.ParentRef
			} else {
				result.Rows[index].Errors = append(result.Rows[index].Errors, "父构件引用未知")
			}
		}
		item := domain.RiggingItem{ID: ids[row.TempID], SessionID: snap.Session.ID, ParentItemID: parentID, ItemType: row.ItemType, Label: strings.TrimSpace(row.Label), Location: strings.TrimSpace(row.Location), RatedLoadKg: row.RatedLoadKg, PlannedLoadKg: row.PlannedLoadKg, InspectionStandard: strings.TrimSpace(row.InspectionStandard), Revision: 1, Active: true}
		items[index] = item
		if err := domain.ValidateItem(item); err != nil {
			result.Rows[index].Errors = append(result.Rows[index].Errors, err.Error())
		}
	}
	prospective := append(append([]domain.RiggingItem{}, snap.Items...), items...)
	byID := map[string]domain.RiggingItem{}
	rowByID := map[string]int{}
	childRows := map[string][]int{}
	childLoads := map[string]float64{}
	for _, item := range prospective {
		if item.Active {
			byID[item.ID] = item
			if item.ParentItemID != "" {
				childLoads[item.ParentItemID] += item.PlannedLoadKg
			}
		}
	}
	for index, item := range items {
		rowByID[item.ID] = index
		if item.ParentItemID != "" {
			childRows[item.ParentItemID] = append(childRows[item.ParentItemID], index)
		}
		seen := map[string]bool{item.ID: true}
		for parent := item.ParentItemID; parent != ""; parent = byID[parent].ParentItemID {
			if seen[parent] {
				result.Rows[index].Errors = append(result.Rows[index].Errors, "父子关系形成循环")
				break
			}
			seen[parent] = true
			if byID[parent].ID == "" {
				break
			}
		}
	}
	for parentID, total := range childLoads {
		parent := byID[parentID]
		if parent.ID == "" || total <= parent.RatedLoadKg {
			continue
		}
		message := fmt.Sprintf("父级 %s 的子项合计 %.2fkg 超过额定载荷 %.2fkg", parent.Label, total, parent.RatedLoadKg)
		if index, ok := rowByID[parentID]; ok {
			result.Rows[index].Errors = append(result.Rows[index].Errors, message)
		}
		for _, index := range childRows[parentID] {
			result.Rows[index].Errors = append(result.Rows[index].Errors, message)
		}
	}
	if err := domain.ValidateGroupLoads(prospective); err != nil && !strings.Contains(err.Error(), "子构件合计载荷") && !strings.Contains(err.Error(), "形成循环") {
		for index := range result.Rows {
			result.Rows[index].Errors = append(result.Rows[index].Errors, err.Error())
		}
	}
	result.Valid = true
	for _, row := range result.Rows {
		result.Valid = result.Valid && len(row.Errors) == 0
	}
	result.Load = domain.CalculateLoadSummary(prospective)
	return items, result
}

func joinRowErrors(rows []RowValidation) string {
	var errors []string
	for _, row := range rows {
		if len(row.Errors) > 0 {
			errors = append(errors, fmt.Sprintf("第 %d 行：%s", row.Row, strings.Join(row.Errors, "、")))
		}
	}
	return strings.Join(errors, "；")
}

type ReviseItemCommand struct {
	WriteMeta
	ParentItemID       string          `json:"parentItemId"`
	ItemType           domain.ItemType `json:"itemType"`
	Label              string          `json:"label"`
	Location           string          `json:"location"`
	RatedLoadKg        float64         `json:"ratedLoadKg"`
	PlannedLoadKg      float64         `json:"plannedLoadKg"`
	InspectionStandard string          `json:"inspectionStandard"`
}

func (s *Service) ReviseItem(ctx context.Context, sessionID, itemID string, c ReviseItemCommand) (domain.RiggingItem, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.RiggingItem{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return domain.RiggingItem{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if snap.Session.Status != domain.StatusDraft {
			return storage.CommandResult{}, fmt.Errorf("%w: 仅草拟批次可修订构件", domain.ErrInvalidState)
		}
		var old domain.RiggingItem
		for _, item := range snap.Items {
			if item.ID == itemID && item.Active {
				old = item
			}
		}
		if old.ID == "" {
			return storage.CommandResult{}, domain.ErrNotFound
		}
		for _, inspection := range snap.Inspections {
			if inspection.ItemID == itemID {
				return storage.CommandResult{}, fmt.Errorf("%w: 已存在检验事实的构件不能修订", domain.ErrInvalidState)
			}
		}
		revised := domain.RiggingItem{ID: uuid.NewString(), SessionID: sessionID, ParentItemID: c.ParentItemID, ItemType: c.ItemType, Label: strings.TrimSpace(c.Label), Location: strings.TrimSpace(c.Location), RatedLoadKg: c.RatedLoadKg, PlannedLoadKg: c.PlannedLoadKg, InspectionStandard: strings.TrimSpace(c.InspectionStandard), Revision: old.Revision + 1, SupersedesID: old.ID, SourceSessionID: old.SourceSessionID, SourceItemID: old.SourceItemID, Active: true}
		if revised.ParentItemID == revised.ID || revised.ParentItemID == old.ID {
			return storage.CommandResult{}, fmt.Errorf("%w: 构件不能以自身为父项", domain.ErrInvalidInput)
		}
		prospective := make([]domain.RiggingItem, 0, len(snap.Items)+1)
		for _, item := range snap.Items {
			if item.ID == old.ID {
				item.Active = false
			}
			if item.Active && item.ParentItemID == old.ID {
				item.ParentItemID = revised.ID
			}
			prospective = append(prospective, item)
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
		response, _ := json.Marshal(revised)
		return mutation(response, "item.revised", revised), nil
	})
	if err != nil {
		return domain.RiggingItem{}, err
	}
	var out domain.RiggingItem
	err = json.Unmarshal(b, &out)
	return out, err
}

type RetireItemCommand struct{ WriteMeta }

type RetireItemResult struct {
	ItemID  string `json:"itemId"`
	Version int64  `json:"version"`
}

func (s *Service) RetireItem(ctx context.Context, sessionID, itemID string, c RetireItemCommand) (RetireItemResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return RetireItemResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return RetireItemResult{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if snap.Session.Status != domain.StatusDraft {
			return storage.CommandResult{}, fmt.Errorf("%w: 仅草拟批次可退役构件", domain.ErrInvalidState)
		}
		found := false
		var children []string
		for _, item := range snap.Items {
			found = found || (item.ID == itemID && item.Active)
			if item.Active && item.ParentItemID == itemID {
				children = append(children, item.ID)
			}
		}
		if !found {
			return storage.CommandResult{}, domain.ErrNotFound
		}
		if len(children) > 0 {
			return storage.CommandResult{}, fmt.Errorf("%w: 仍有活动子项 %s，请先重新挂接或退役", domain.ErrInvalidState, strings.Join(children, ","))
		}
		for _, inspection := range snap.Inspections {
			if inspection.ItemID == itemID {
				return storage.CommandResult{}, fmt.Errorf("%w: 已存在检验记录的构件不能退役", domain.ErrInvalidState)
			}
		}
		if err = u.RetireItem(sessionID, itemID); err != nil {
			return storage.CommandResult{}, err
		}
		result := RetireItemResult{ItemID: itemID, Version: c.ExpectedVersion + 1}
		response, _ := json.Marshal(result)
		return mutation(response, "item.retired", result), nil
	})
	if err != nil {
		return RetireItemResult{}, err
	}
	var out RetireItemResult
	err = json.Unmarshal(b, &out)
	return out, err
}

type BatchInspectionEntry struct {
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

type TaskValidation struct {
	Index     int              `json:"index"`
	ItemID    string           `json:"itemId"`
	CheckType domain.CheckType `json:"checkType"`
	Errors    []string         `json:"errors"`
}

type BatchInspectionPreflight struct {
	Valid bool             `json:"valid"`
	Tasks []TaskValidation `json:"tasks"`
}

type BatchInspectionPreflightCommand struct {
	ExpectedVersion int64                  `json:"expectedVersion"`
	Entries         []BatchInspectionEntry `json:"entries"`
}

func (s *Service) PreflightBatchInspections(ctx context.Context, sessionID string, c BatchInspectionPreflightCommand) (BatchInspectionPreflight, error) {
	snap, err := s.store.Snapshot(ctx, sessionID)
	if err != nil {
		return BatchInspectionPreflight{}, err
	}
	if snap.Session.Version != c.ExpectedVersion {
		return BatchInspectionPreflight{}, fmt.Errorf("%w: 当前版本为 %d", domain.ErrVersionConflict, snap.Session.Version)
	}
	if err = domain.RequireMutable(snap.Session); err != nil {
		return BatchInspectionPreflight{}, err
	}
	cache := &s.inspectionPreflightCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.sessionID == sessionID {
		return cloneBatchInspectionPreflight(cache.result), nil
	}
	result := validateBatchInspections(snap, c.Entries, time.Now().UTC())
	cache.sessionID = sessionID
	cache.result = cloneBatchInspectionPreflight(result)
	return cloneBatchInspectionPreflight(result), nil
}

func cloneBatchInspectionPreflight(value BatchInspectionPreflight) BatchInspectionPreflight {
	cloned := BatchInspectionPreflight{Valid: value.Valid, Tasks: make([]TaskValidation, len(value.Tasks))}
	for index, task := range value.Tasks {
		cloned.Tasks[index] = task
		cloned.Tasks[index].Errors = append([]string(nil), task.Errors...)
	}
	return cloned
}

type BatchInspectionCommand struct {
	WriteMeta
	Entries []BatchInspectionEntry `json:"entries"`
}

type BatchInspectionResult struct {
	Inspections []domain.Inspection `json:"inspections"`
	Hazards     []domain.Hazard     `json:"hazards"`
	Version     int64               `json:"version"`
}

func (s *Service) SubmitBatchInspections(ctx context.Context, sessionID string, c BatchInspectionCommand) (BatchInspectionResult, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return BatchInspectionResult{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleTechnician); err != nil {
		return BatchInspectionResult{}, err
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		snap, err := u.Snapshot(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(snap.Session); err != nil {
			return storage.CommandResult{}, err
		}
		check := validateBatchInspections(snap, c.Entries, time.Now().UTC())
		if !check.Valid {
			var parts []string
			for _, task := range check.Tasks {
				if len(task.Errors) > 0 {
					parts = append(parts, fmt.Sprintf("任务 %s/%s：%s", task.ItemID, task.CheckType, strings.Join(task.Errors, "、")))
				}
			}
			return storage.CommandResult{}, fmt.Errorf("%w: %s", domain.ErrIncomplete, strings.Join(parts, "；"))
		}
		result := BatchInspectionResult{Version: c.ExpectedVersion + 1}
		now := time.Now().UTC()
		for _, entry := range c.Entries {
			inspection := domain.Inspection{ID: uuid.NewString(), SessionID: sessionID, ItemID: entry.ItemID, Round: 1, CheckType: entry.CheckType, MeasuredValue: strings.TrimSpace(entry.MeasuredValue), Verdict: entry.Verdict, EvidenceRef: strings.TrimSpace(entry.EvidenceRef), InspectorName: strings.TrimSpace(entry.InspectorName), RecordedAt: now}
			if err = u.InsertInspection(inspection); err != nil {
				return storage.CommandResult{}, err
			}
			result.Inspections = append(result.Inspections, inspection)
			if entry.Verdict == domain.VerdictFail {
				hazard := domain.Hazard{ID: uuid.NewString(), SessionID: sessionID, InspectionID: inspection.ID, ItemID: entry.ItemID, Severity: entry.Severity, Scope: strings.TrimSpace(entry.Scope), RequiredAction: strings.TrimSpace(entry.RequiredAction), Assignee: strings.TrimSpace(entry.Assignee), DueAt: entry.DueAt.UTC(), Status: domain.HazardOpen}
				if err = u.InsertHazard(hazard); err != nil {
					return storage.CommandResult{}, err
				}
				result.Hazards = append(result.Hazards, hazard)
			}
		}
		if err = u.MarkInspecting(sessionID); err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(result)
		return mutation(response, "inspections.bulk_recorded", result), nil
	})
	if err != nil {
		return BatchInspectionResult{}, err
	}
	var out BatchInspectionResult
	err = json.Unmarshal(b, &out)
	return out, err
}

func validateBatchInspections(snap domain.Snapshot, entries []BatchInspectionEntry, now time.Time) BatchInspectionPreflight {
	result := BatchInspectionPreflight{Tasks: make([]TaskValidation, len(entries)), Valid: len(entries) > 0 && len(entries) <= 500}
	active := map[string]bool{}
	lineage := map[string]string{}
	byID := map[string]domain.RiggingItem{}
	for _, item := range snap.Items {
		byID[item.ID] = item
	}
	for _, item := range snap.Items {
		if item.Active {
			active[item.ID] = true
			for current := item; current.ID != ""; {
				lineage[current.ID] = item.ID
				if current.SupersedesID == "" {
					break
				}
				current = byID[current.SupersedesID]
			}
		}
	}
	completed := map[string]bool{}
	for _, inspection := range snap.Inspections {
		if inspection.Round == 1 {
			if activeID := lineage[inspection.ItemID]; activeID != "" {
				completed[activeID+":"+string(inspection.CheckType)] = true
			}
		}
	}
	seen := map[string]int{}
	for index, entry := range entries {
		task := TaskValidation{Index: index, ItemID: entry.ItemID, CheckType: entry.CheckType}
		key := entry.ItemID + ":" + string(entry.CheckType)
		if !active[entry.ItemID] {
			task.Errors = append(task.Errors, "构件不存在或已停用")
		}
		if completed[key] {
			task.Errors = append(task.Errors, "任务已由其他操作者提交")
		}
		if prior, ok := seen[key]; ok {
			task.Errors = append(task.Errors, fmt.Sprintf("与批次内第 %d 项重复", prior+1))
		} else {
			seen[key] = index
		}
		inspection := domain.Inspection{ItemID: entry.ItemID, Round: 1, CheckType: entry.CheckType, MeasuredValue: entry.MeasuredValue, Verdict: entry.Verdict, EvidenceRef: entry.EvidenceRef, InspectorName: entry.InspectorName}
		if err := domain.ValidateInspection(inspection, false); err != nil {
			task.Errors = append(task.Errors, err.Error())
		}
		if entry.Verdict == domain.VerdictFail {
			if entry.Severity != domain.SeverityBlocking && entry.Severity != domain.SeverityAdvisory {
				task.Errors = append(task.Errors, "不合格项必须指定隐患级别")
			}
			if strings.TrimSpace(entry.Scope) == "" || strings.TrimSpace(entry.RequiredAction) == "" {
				task.Errors = append(task.Errors, "不合格项必须填写影响范围和处置要求")
			}
			if err := domain.ValidateHazardDeadline(entry.Assignee, entry.DueAt, snap.Session.ScheduledAt, now); err != nil {
				task.Errors = append(task.Errors, err.Error())
			}
		}
		if len(task.Errors) > 0 {
			result.Valid = false
		}
		result.Tasks[index] = task
	}
	return result
}

type ReassignHazardCommand struct {
	WriteMeta
	Assignee string    `json:"assignee"`
	DueAt    time.Time `json:"dueAt"`
	Reason   string    `json:"reason"`
}

func (s *Service) ReassignHazard(ctx context.Context, sessionID, hazardID string, c ReassignHazardCommand) (domain.HazardAssignment, error) {
	if err := c.WriteMeta.validate(); err != nil {
		return domain.HazardAssignment{}, err
	}
	if err := domain.RequireRole(c.Role, domain.RoleSupervisor); err != nil {
		return domain.HazardAssignment{}, err
	}
	if strings.TrimSpace(c.Reason) == "" {
		return domain.HazardAssignment{}, fmt.Errorf("%w: 转派原因不能为空", domain.ErrInvalidInput)
	}
	b, _, err := s.store.Command(ctx, commandMeta(sessionID, c.WriteMeta, c), func(u *storage.Unit) (storage.CommandResult, error) {
		session, err := u.Session(sessionID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.RequireMutable(session); err != nil {
			return storage.CommandResult{}, err
		}
		if err = domain.ValidateHazardDeadline(c.Assignee, c.DueAt, session.ScheduledAt, time.Now().UTC()); err != nil {
			return storage.CommandResult{}, err
		}
		hazard, err := u.Hazard(hazardID)
		if err != nil {
			return storage.CommandResult{}, err
		}
		if hazard.SessionID != sessionID {
			return storage.CommandResult{}, domain.ErrNotFound
		}
		assignment, err := u.ReassignHazard(hazardID, strings.TrimSpace(c.Assignee), c.DueAt.UTC(), c.Actor, strings.TrimSpace(c.Reason))
		if err != nil {
			return storage.CommandResult{}, err
		}
		response, _ := json.Marshal(assignment)
		return mutation(response, "hazard.reassigned", assignment), nil
	})
	if err != nil {
		return domain.HazardAssignment{}, err
	}
	var out domain.HazardAssignment
	err = json.Unmarshal(b, &out)
	return out, err
}
