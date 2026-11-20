package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// An item is "unconfirmed" when it is shown in the library (imported or a
// later stage) although its current attempt never reached 'imported':
// Jellyfin, Seerr or the presence check said so, not an import. Only
// Sonarr/Radarr's file confirms such a claim. On 2026-11-20 Jellyfin's
// placeholders for missing episodes made 101 items look available this way.

const unconfirmedItem = `
	(SELECT ordinal FROM stages WHERE key = mi.current_stage) >=
		(SELECT ordinal FROM stages WHERE key = 'imported')
	AND NOT EXISTS (SELECT 1 FROM stage_transitions st
		WHERE st.media_item_id = mi.id AND st.cycle = mi.current_cycle AND st.stage = 'imported')`

// UnconfirmedRequests returns the requests, other than canceled or failed
// ones, with at least one unconfirmed item, oldest first. A request whose
// every item only looks available shows as completed, so the open-request
// list misses it.
func (s *Store) UnconfirmedRequests(ctx context.Context) ([]Request, error) {
	rows, err := s.db.QueryContext(ctx, reqSelect+`
		WHERE status NOT IN ('canceled','failed') AND EXISTS (
			SELECT 1 FROM media_items mi WHERE mi.request_id = requests.id AND `+unconfirmedItem+`)
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RewindUnconfirmed takes back the library stages of an item that is not on
// disk. In every attempt without an import, the stages past import
// (transcode, available, notified) are deleted, and the Jellyfin and presence
// sightings of those attempts no longer block a new one. The item falls back
// to the furthest stage its current attempt really reached ('requested' when
// none). The deleted 'notified' rows no longer count as announced, so the real
// arrival gets its notice. Returns the new stage and how many rows went.
func (s *Store) RewindUnconfirmed(ctx context.Context, itemID int64) (string, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var cur int
	if err := tx.QueryRowContext(ctx, `SELECT current_cycle FROM media_items WHERE id = ?`, itemID).Scan(&cur); err != nil {
		return "", 0, fmt.Errorf("rewind item %d: %w", itemID, err)
	}

	cycles, err := unimportedLibraryCycles(ctx, tx, itemID)
	if err != nil {
		return "", 0, err
	}

	removed := 0
	for _, c := range cycles {
		res, err := tx.ExecContext(ctx, `
			DELETE FROM stage_transitions WHERE media_item_id = ? AND cycle = ? AND stage IN `+pastImport,
			itemID, c)
		if err != nil {
			return "", 0, fmt.Errorf("rewind item %d cycle %d: %w", itemID, c, err)
		}
		n, _ := res.RowsAffected()
		removed += int(n)
		// The event stays as a record; only its key is freed so the poller can
		// report the real file of this attempt.
		if _, err := tx.ExecContext(ctx, `UPDATE events SET dedupe_key = NULL WHERE dedupe_key IN (?, ?)`,
			fmt.Sprintf("jellyfin:avail:%d:%d", itemID, c),
			fmt.Sprintf("presence:avail:%d:%d", itemID, c)); err != nil {
			return "", 0, fmt.Errorf("rewind item %d: release sightings: %w", itemID, err)
		}
	}

	stage := "requested"
	err = tx.QueryRowContext(ctx, `
		SELECT st.stage FROM stage_transitions st JOIN stages sg ON sg.key = st.stage
		WHERE st.media_item_id = ? AND st.cycle = ? ORDER BY sg.ordinal DESC LIMIT 1`, itemID, cur).Scan(&stage)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	// jellyfin_item_id points at the placeholder; the real file sets it anew.
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_items SET current_stage = ?, jellyfin_item_id = NULL, stuck_since = NULL,
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`, stage, itemID); err != nil {
		return "", 0, err
	}
	return stage, removed, tx.Commit()
}

// pastImport lists the stages after 'imported': transcode, available, notified.
const pastImport = `(SELECT key FROM stages WHERE ordinal >
	(SELECT ordinal FROM stages WHERE key = 'imported'))`

// unimportedLibraryCycles returns the attempts of an item that carry a stage
// past import without an 'imported' of their own.
func unimportedLibraryCycles(ctx context.Context, tx *sql.Tx, itemID int64) ([]int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT st.cycle FROM stage_transitions st
		WHERE st.media_item_id = ? AND st.stage IN `+pastImport+`
		AND NOT EXISTS (SELECT 1 FROM stage_transitions i
			WHERE i.media_item_id = st.media_item_id AND i.cycle = st.cycle AND i.stage = 'imported')`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cycles []int
	for rows.Next() {
		var c int
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cycles = append(cycles, c)
	}
	return cycles, rows.Err()
}
