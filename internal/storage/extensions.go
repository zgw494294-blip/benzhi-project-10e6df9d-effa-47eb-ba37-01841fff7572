package storage

import (
	"database/sql"
	"time"

	"stage-rigging-clearance/internal/domain"
)

func (u *Unit) InsertItems(items []domain.RiggingItem) error {
	for _, item := range items {
		if err := u.InsertItem(item); err != nil {
			return err
		}
	}
	return nil
}

func (u *Unit) ReparentChildren(sessionID, oldParentID, newParentID string) error {
	_, err := u.tx.Exec(`UPDATE rigging_items SET parent_item_id=? WHERE session_id=? AND parent_item_id=? AND active=1`, newParentID, sessionID, oldParentID)
	return err
}

func (u *Unit) RetireItem(sessionID, itemID string) error {
	res, err := u.tx.Exec(`UPDATE rigging_items SET active=0 WHERE id=? AND session_id=? AND active=1`, itemID, sessionID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (u *Unit) InsertRemediation(r domain.RemediationRecord) error {
	_, err := u.tx.Exec(`INSERT INTO remediation_records(id,hazard_id,round,note,evidence_ref,actor,item_id,submitted_at) VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.HazardID, r.Round, r.Note, r.EvidenceRef, r.Actor, r.ItemID, formatTime(r.SubmittedAt))
	return err
}

func (u *Unit) NextRemediationRound(hazardID string) (int, error) {
	var round sql.NullInt64
	if err := u.tx.QueryRow(`SELECT MAX(round) FROM remediation_records WHERE hazard_id=?`, hazardID).Scan(&round); err != nil {
		return 0, err
	}
	return int(round.Int64) + 1, nil
}

func (u *Unit) CompleteReinspection(hazardID, inspectionID string, passed bool) error {
	status := domain.HazardOpen
	closedAt := ""
	if passed {
		status = domain.HazardClosed
		closedAt = formatTime(u.now)
	}
	res, err := u.tx.Exec(`UPDATE hazards SET status=?,reinspection_id=?,closed_at=? WHERE id=? AND status=?`, status, inspectionID, closedAt, hazardID, domain.HazardAwaitingReinspection)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrInvalidState
	}
	_, err = u.tx.Exec(`UPDATE remediation_records SET reinspection_id=? WHERE hazard_id=? AND round=(SELECT MAX(round) FROM remediation_records WHERE hazard_id=?)`, inspectionID, hazardID, hazardID)
	return err
}

func (u *Unit) ReassignHazard(hazardID, assignee string, dueAt time.Time, actor, reason string) (domain.HazardAssignment, error) {
	h, err := u.Hazard(hazardID)
	if err != nil {
		return domain.HazardAssignment{}, err
	}
	if h.Status == domain.HazardClosed {
		return domain.HazardAssignment{}, domain.ErrInvalidState
	}
	res, err := u.tx.Exec(`UPDATE hazards SET assignee=?,due_at=? WHERE id=? AND status<>?`, assignee, formatTime(dueAt), hazardID, domain.HazardClosed)
	if err != nil {
		return domain.HazardAssignment{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.HazardAssignment{}, domain.ErrInvalidState
	}
	a := domain.HazardAssignment{HazardID: hazardID, OldAssignee: h.Assignee, NewAssignee: assignee, OldDueAt: h.DueAt, NewDueAt: dueAt, Actor: actor, Reason: reason, ChangedAt: u.now}
	result, err := u.tx.Exec(`INSERT INTO hazard_assignments(hazard_id,old_assignee,new_assignee,old_due_at,new_due_at,actor,reason,changed_at) VALUES(?,?,?,?,?,?,?,?)`, a.HazardID, a.OldAssignee, a.NewAssignee, formatTime(a.OldDueAt), formatTime(a.NewDueAt), a.Actor, a.Reason, formatTime(a.ChangedAt))
	if err != nil {
		return domain.HazardAssignment{}, err
	}
	a.ID, _ = result.LastInsertId()
	return a, nil
}
