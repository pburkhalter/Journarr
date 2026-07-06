package store

import "context"

// MarkStuck sets stuck_since on active-request items that have sat in their
// current non-terminal stage longer than the per-stage threshold (seconds)
// and aren't already flagged. Returns how many were newly flagged. ApplyStage
// clears stuck_since whenever an item advances, so a flag means "no progress
// since". 'available'/'notified' and error items are never flagged here.
func (s *Store) MarkStuck(ctx context.Context, thresholds map[string]int) (int64, error) {
	var total int64
	for stage, secs := range thresholds {
		res, err := s.db.ExecContext(ctx, `
			UPDATE media_items SET stuck_since = CURRENT_TIMESTAMP
			WHERE current_stage = ? AND stuck_since IS NULL
			AND (last_error IS NULL OR last_error = '')
			AND request_id IN (SELECT id FROM requests WHERE status = 'active')
			AND updated_at < datetime('now', ?)`,
			stage, "-"+itoa(secs)+" seconds")
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// CountStuck returns the number of currently-stuck items (for the header).
func (s *Store) CountStuck(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_items WHERE stuck_since IS NOT NULL`).Scan(&n)
	return n, err
}
