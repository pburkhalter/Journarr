package store

import (
	"context"
	"time"
)

// MoviesForReleaseCheck returns active movie items still parked at an early
// stage (requested/approved) — the candidates for the "waiting for release"
// check against Radarr. Once a movie is grabbed it advances past these stages
// and drops out of the set.
func (s *Store) MoviesForReleaseCheck(ctx context.Context) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, itemSelect+`
		WHERE media_type = 'movie' AND tmdb_id IS NOT NULL
		AND current_stage IN ('requested','approved')
		AND request_id IN (SELECT id FROM requests WHERE status = 'active')
		ORDER BY id`)
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

// SetAwaitingRelease marks an item as waiting for its release date. It also
// clears any stuck flag — a film that isn't out yet isn't stalled.
func (s *Store) SetAwaitingRelease(ctx context.Context, itemID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_items SET awaiting_release_at = ?, stuck_since = NULL
		WHERE id = ?`, sqliteTime(at), itemID)
	return err
}

// ClearAwaitingRelease removes the waiting-for-release annotation once Radarr
// reports the movie available (or it's gone from Radarr).
func (s *Store) ClearAwaitingRelease(ctx context.Context, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_items SET awaiting_release_at = NULL
		WHERE id = ? AND awaiting_release_at IS NOT NULL`, itemID)
	return err
}
