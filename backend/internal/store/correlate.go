package store

import (
	"context"
	"database/sql"
)

// DownloadClientIDForItemCycle returns the client download id linked to an
// item within a cycle ("" when none) — used to distinguish an idempotent
// grab replay from a genuine re-grab.
func (s *Store) DownloadClientIDForItemCycle(ctx context.Context, itemID int64, cycle int) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT d.client_download_id FROM download_items di
		JOIN downloads d ON d.id = di.download_id
		WHERE di.media_item_id = ? AND di.cycle = ?
		ORDER BY di.download_id DESC LIMIT 1`, itemID, cycle).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// UnimportedItemCount counts linked items that have no 'imported' transition
// in the cycle this download belongs to — precise even when an item has
// since moved on to a newer cycle with a different download.
func (s *Store) UnimportedItemCount(ctx context.Context, downloadID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM download_items di
		WHERE di.download_id = ?
		AND NOT EXISTS (
			SELECT 1 FROM stage_transitions st
			WHERE st.media_item_id = di.media_item_id
			AND st.cycle = di.cycle AND st.stage = 'imported')`, downloadID).Scan(&n)
	return n, err
}

// CycleForItemDownload reports which cycle a download is linked to for an
// item (0 = not linked). Late-arriving events for an old download must apply
// to their own cycle, never to the item's current one.
func (s *Store) CycleForItemDownload(ctx context.Context, itemID int64, clientDownloadID string) (int, error) {
	var cycle int
	err := s.db.QueryRowContext(ctx, `
		SELECT di.cycle FROM download_items di
		JOIN downloads d ON d.id = di.download_id
		WHERE di.media_item_id = ? AND d.client_download_id = ?
		ORDER BY di.cycle DESC LIMIT 1`, itemID, NormalizeDownloadID(clientDownloadID)).Scan(&cycle)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return cycle, err
}

// GetPollCursor / SetPollCursor persist per-poller resume positions.
func (s *Store) GetPollCursor(ctx context.Context, source string) (string, error) {
	var c sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor FROM poll_state WHERE source = ?`, source).Scan(&c)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return c.String, err
}

func (s *Store) SetPollCursor(ctx context.Context, source, cursor string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO poll_state (source, cursor, last_run) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source) DO UPDATE SET cursor = excluded.cursor, last_run = CURRENT_TIMESTAMP`,
		source, cursor)
	return err
}
