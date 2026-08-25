package domain

import "fmt"

func CalculateLoadSummary(items []RiggingItem) LoadSummary {
	r := LoadSummary{MinimumMarginKg: 0}
	first := true
	for _, item := range items {
		if !item.Active {
			continue
		}
		r.ItemCount++
		r.RatedTotalKg += item.RatedLoadKg
		r.PlannedTotalKg += item.PlannedLoadKg
		margin := item.MarginKg()
		if first || margin < r.MinimumMarginKg {
			r.MinimumMarginKg = margin
			first = false
		}
		if margin < 0 {
			r.Overloaded = true
		}
	}
	return r
}

func ValidateGroupLoads(items []RiggingItem) error {
	children := map[string]float64{}
	byID := map[string]RiggingItem{}
	for _, i := range items {
		if !i.Active {
			continue
		}
		if err := ValidateItem(i); err != nil {
			return err
		}
		byID[i.ID] = i
		if i.ParentItemID != "" {
			children[i.ParentItemID] += i.PlannedLoadKg
		}
	}
	for parent, total := range children {
		p, ok := byID[parent]
		if !ok {
			return fmt.Errorf("%w: 父构件 %s 不存在", ErrInvalidInput, parent)
		}
		if total > p.RatedLoadKg {
			return fmt.Errorf("%w: 子构件合计载荷超过 %s 的额定载荷", ErrIncomplete, p.Label)
		}
	}
	for _, item := range items {
		if !item.Active || item.ParentItemID == "" {
			continue
		}
		seen := map[string]bool{item.ID: true}
		parent := item.ParentItemID
		for parent != "" {
			if seen[parent] {
				return fmt.Errorf("%w: 构件父子关系形成循环", ErrInvalidInput)
			}
			seen[parent] = true
			p, ok := byID[parent]
			if !ok {
				break
			}
			parent = p.ParentItemID
		}
	}
	return nil
}
