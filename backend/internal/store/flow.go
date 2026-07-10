package store

import (
	"context"
	"time"
)

// sqliteTime formats a time to match SQLite's CURRENT_TIMESTAMP ("YYYY-MM-DD
// HH:MM:SS", UTC) so `run_after <= CURRENT_TIMESTAMP` string comparisons are
// correct (the driver's default time.Time serialization is not comparable).
func sqliteTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// FlowSettings are the control-plane toggles. Defaults reproduce today's
// behavior (everything Journarr-owned is off).
func (s *Store) GetFlowSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM flow_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetFlowSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO flow_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value)
	return err
}

// FlowTask is one durable unit of control-plane work.
type FlowTask struct {
	ID         int64
	Kind       string
	TargetType string
	TargetID   int64
	Payload    string
	Attempts   int
}

// EnqueueFlowTask appends a task. A non-empty dedupeKey coalesces re-triggers:
// while a pending task with that key exists the insert is ignored (returns
// false). The key is cleared on finish so the same logical task can recur.
func (s *Store) EnqueueFlowTask(ctx context.Context, kind, targetType string, targetID int64, payload, dedupeKey string, runAfter time.Time) (bool, error) {
	var key any
	if dedupeKey != "" {
		key = dedupeKey
	}
	var tt any
	if targetType != "" {
		tt = targetType
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO flow_tasks (kind, target_type, target_id, payload, dedupe_key, run_after)
		VALUES (?, ?, NULLIF(?, 0), ?, ?, ?)`,
		kind, tt, targetID, payload, key, sqliteTime(runAfter))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClaimFlowTasks returns due pending tasks in insert order. A single worker
// drains these, so no row-level locking is needed.
func (s *Store) ClaimFlowTasks(ctx context.Context, limit int) ([]FlowTask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, COALESCE(target_type,''), COALESCE(target_id,0), COALESCE(payload,''), attempts
		FROM flow_tasks
		WHERE status = 'pending' AND (run_after IS NULL OR run_after <= CURRENT_TIMESTAMP)
		ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlowTask
	for rows.Next() {
		var t FlowTask
		if err := rows.Scan(&t.ID, &t.Kind, &t.TargetType, &t.TargetID, &t.Payload, &t.Attempts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FinishFlowTask marks a task terminal and clears its dedupe key so the same
// logical task can be enqueued again later.
func (s *Store) FinishFlowTask(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE flow_tasks SET status = ?, dedupe_key = NULL, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, id)
	return err
}

// ReleaseFlowTaskDedupe clears a running task's dedupe key without finishing it,
// so a concurrent completion can enqueue a follow-up for the same target (used
// to catch stragglers that arrive during a long-running task's network call).
func (s *Store) ReleaseFlowTaskDedupe(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE flow_tasks SET dedupe_key = NULL WHERE id = ?`, id)
	return err
}

// RescheduleFlowTask bumps the attempt count and defers a retry.
func (s *Store) RescheduleFlowTask(ctx context.Context, id int64, runAfter time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE flow_tasks SET attempts = attempts + 1, run_after = ? WHERE id = ?`,
		sqliteTime(runAfter), id)
	return err
}

// ClearStuckItem clears the stuck flag on one item — used after auto-retry so
// the sweeper doesn't re-fire until MarkStuck re-flags it (bounds retry cadence
// to the stuck threshold rather than the sweep interval).
func (s *Store) ClearStuckItem(ctx context.Context, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE media_items SET stuck_since = NULL WHERE id = ?`, itemID)
	return err
}

// ReapFlowTasks prunes finished tasks past the retention window.
func (s *Store) ReapFlowTasks(ctx context.Context, olderThanDays int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM flow_tasks WHERE status IN ('done','failed')
		AND finished_at < datetime('now', ?)`, "-"+itoa(olderThanDays)+" days")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StuckItemsForRetry returns active items flagged stuck for at least minAge —
// the working set for the auto-retry-stuck rule.
func (s *Store) StuckItemsForRetry(ctx context.Context, minAge time.Duration) ([]MediaItem, error) {
	cutoff := sqliteTime(time.Now().Add(-minAge))
	rows, err := s.db.QueryContext(ctx, itemSelect+`
		WHERE stuck_since IS NOT NULL AND stuck_since <= ?
		AND request_id IN (SELECT id FROM requests WHERE status IN ('active','partial','failed'))
		ORDER BY id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaItem{}
	for rows.Next() {
		m, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}
