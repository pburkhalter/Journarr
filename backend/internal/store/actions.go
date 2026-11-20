package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pburkhalter/journarr/internal/audit"
)

// Action is one row of the interventions audit.
type Action struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	TargetType string     `json:"target_type,omitempty"`
	TargetID   int64      `json:"target_id,omitempty"`
	Status     string     `json:"status"`
	Detail     string     `json:"detail,omitempty"`
	Actor      string     `json:"actor"`
	Params     string     `json:"params,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// InsertAction records an intervention (retry/cancel/jellyfin_scan/...) as
// pending and returns its id. The actor comes from ctx (audit.WithActor);
// params, when not nil, is stored as JSON. FinishAction closes the row.
func (s *Store) InsertAction(ctx context.Context, kind, targetType string, targetID int64, params any) (int64, error) {
	var p *string
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return 0, err
		}
		v := string(b)
		p = &v
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO actions (kind, target_type, target_id, status, actor, params)
		VALUES (?, ?, NULLIF(?, 0), 'pending', ?, ?)`, kind, targetType, targetID, audit.Actor(ctx), p)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishAction(ctx context.Context, id int64, status, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE actions SET status = ?, detail = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, detail, id)
	return err
}

// RecentActions returns the newest audit rows, newest first.
func (s *Store) RecentActions(ctx context.Context, limit int) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, COALESCE(target_type, ''), COALESCE(target_id, 0), status,
		       COALESCE(detail, ''), actor, COALESCE(params, ''), created_at, finished_at
		FROM actions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.ID, &a.Kind, &a.TargetType, &a.TargetID, &a.Status,
			&a.Detail, &a.Actor, &a.Params, &a.CreatedAt, &a.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
