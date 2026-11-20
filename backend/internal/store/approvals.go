package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Approval is a removal request waiting for, or past, the owner's decision.
type Approval struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Target      string     `json:"target"` // JSON
	TargetKey   string     `json:"target_key"`
	Title       string     `json:"title"`
	Plan        string     `json:"plan"` // JSON
	PlanHash    string     `json:"plan_hash"`
	Reason      string     `json:"reason,omitempty"`
	RequestedBy string     `json:"requested_by"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	Result      string     `json:"result,omitempty"` // JSON
}

// ErrApprovalPending means the title already has a pending removal request.
var ErrApprovalPending = errors.New("a removal request for this title is already pending")

const approvalSelect = `SELECT id, kind, target, target_key, title, plan, plan_hash, reason, requested_by,
	status, created_at, expires_at, decided_at, COALESCE(decided_by, ''), COALESCE(result, '') FROM approvals`

func scanApproval(row rowScanner) (*Approval, error) {
	var a Approval
	var decided sql.NullTime
	if err := row.Scan(&a.ID, &a.Kind, &a.Target, &a.TargetKey, &a.Title, &a.Plan, &a.PlanHash, &a.Reason,
		&a.RequestedBy, &a.Status, &a.CreatedAt, &a.ExpiresAt, &decided, &a.DecidedBy, &a.Result); err != nil {
		return nil, err
	}
	if decided.Valid {
		t := decided.Time
		a.DecidedAt = &t
	}
	return &a, nil
}

// InsertApproval stores a new pending request. ErrApprovalPending when the
// title already has one.
func (s *Store) InsertApproval(ctx context.Context, a Approval) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO approvals (kind, target, target_key, title, plan, plan_hash, reason, requested_by, expires_at)
		VALUES ('remove', ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Target, a.TargetKey, a.Title, a.Plan, a.PlanHash, a.Reason, a.RequestedBy, a.ExpiresAt.UTC())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrApprovalPending
		}
		return 0, err
	}
	return res.LastInsertId()
}

// GetApproval returns nil when the id is unknown.
func (s *Store) GetApproval(ctx context.Context, id int64) (*Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx, approvalSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// PendingApprovalFor returns the pending request for a title, or nil.
func (s *Store) PendingApprovalFor(ctx context.Context, targetKey string) (*Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx,
		approvalSelect+` WHERE target_key = ? AND status = 'pending'`, targetKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ListApprovals returns pending requests first, then the newest decided ones.
func (s *Store) ListApprovals(ctx context.Context, onlyPending bool, limit int) ([]Approval, error) {
	q := approvalSelect
	if onlyPending {
		q += ` WHERE status = 'pending'`
	}
	q += ` ORDER BY status = 'pending' DESC, id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// CountPendingApprovals counts requests waiting for a decision.
func (s *Store) CountPendingApprovals(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// DecideApproval moves a pending, unexpired request to status (approved or
// rejected) in one statement, so two clicks cannot both execute it. false
// when it was not pending any more or has expired.
func (s *Store) DecideApproval(ctx context.Context, id int64, status, by string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE approvals SET status = ?, decided_by = ?, decided_at = ?
		WHERE id = ? AND status = 'pending' AND expires_at > ?`, status, by, now.UTC(), id, now.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishApproval records the outcome of an approved request.
func (s *Store) FinishApproval(ctx context.Context, id int64, status, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, result = ? WHERE id = ?`, status, result, id)
	return err
}

// ExpireApprovals marks pending requests past their deadline as expired and
// returns their ids.
func (s *Store) ExpireApprovals(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE approvals SET status = 'expired', decided_at = ?
		WHERE status = 'pending' AND expires_at <= ? RETURNING id`, now.UTC(), now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
