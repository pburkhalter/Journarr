package store

import "context"

// MarkStuck sets stuck_since on active-request items that have sat in their
// current non-terminal stage longer than the per-stage threshold (seconds)
// and aren't already flagged. Returns how many were newly flagged. ApplyStage
// clears stuck_since whenever an item advances, so a flag means "no progress
// since". Items whose linked download progressed recently (byte counters write
// the download row, not the item) are NOT flagged — a large in-flight download
// is healthy, not stuck.
func (s *Store) MarkStuck(ctx context.Context, thresholds map[string]int) (int64, error) {
	var total int64
	for stage, secs := range thresholds {
		cutoff := "-" + itoa(secs) + " seconds"
		res, err := s.db.ExecContext(ctx, `
			UPDATE media_items SET stuck_since = CURRENT_TIMESTAMP
			WHERE current_stage = ? AND stuck_since IS NULL
			AND (last_error IS NULL OR last_error = '')
			AND request_id IN (SELECT id FROM requests WHERE status = 'active')
			AND updated_at < datetime('now', ?)
			AND NOT EXISTS (
				SELECT 1 FROM download_items di
				JOIN downloads d ON d.id = di.download_id
				WHERE di.media_item_id = media_items.id
				AND di.cycle = media_items.current_cycle
				AND d.updated_at >= datetime('now', ?))`,
			stage, cutoff, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ClearStuckForRequest removes stuck flags from a request's items (on cancel,
// so a canceled request doesn't keep inflating the stuck badge).
func (s *Store) ClearStuckForRequest(ctx context.Context, requestID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media_items SET stuck_since = NULL WHERE request_id = ? AND stuck_since IS NOT NULL`, requestID)
	return err
}

// CountStuck returns the number of currently-stuck items in active requests.
func (s *Store) CountStuck(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_items
		WHERE stuck_since IS NOT NULL
		AND request_id IN (SELECT id FROM requests WHERE status = 'active')`).Scan(&n)
	return n, err
}
