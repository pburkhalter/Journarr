package store

import "context"

// InsertAction records an intervention (retry/cancel/jellyfin_scan/...) as
// pending and returns its id. FinishAction closes it with the outcome.
func (s *Store) InsertAction(ctx context.Context, kind, targetType string, targetID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO actions (kind, target_type, target_id, status)
		VALUES (?, ?, NULLIF(?, 0), 'pending')`, kind, targetType, targetID)
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
