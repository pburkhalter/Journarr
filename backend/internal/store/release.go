package store

import (
	"context"
	"database/sql"
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

// TVWaitCandidate is a tv request the waiting poller evaluates against Sonarr.
type TVWaitCandidate struct {
	ID              int64
	TvdbID          int64
	InFlight        bool       // has an item still being worked (not available/notified)
	AwaitingReleaseAt *time.Time // current request-level awaiting date (nil = none)
}

// TVWaitingCandidates returns tv requests eligible for the waiting-for-release
// check: those with a tvdb id that aren't canceled/failed. Each carries whether
// it has in-flight work and its current awaiting stamp, so the poller only
// writes on a real change.
func (s *Store) TVWaitingCandidates(ctx context.Context) ([]TVWaitCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tvdb_id, awaiting_release_at,
			EXISTS (SELECT 1 FROM media_items mi WHERE mi.request_id = requests.id
				AND mi.current_stage NOT IN ('available','notified'))
		FROM requests
		WHERE media_type = 'tv' AND tvdb_id IS NOT NULL
		AND status IN ('active','partial','completed')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TVWaitCandidate{}
	for rows.Next() {
		var c TVWaitCandidate
		var await sql.NullTime
		if err := rows.Scan(&c.ID, &c.TvdbID, &await, &c.InFlight); err != nil {
			return nil, err
		}
		if await.Valid {
			c.AwaitingReleaseAt = &await.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetRequestAwaiting stamps a request as waiting-for-release (tv next airing).
func (s *Store) SetRequestAwaiting(ctx context.Context, reqID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE requests SET awaiting_release_at = ? WHERE id = ?`, sqliteTime(at), reqID)
	return err
}

// ClearRequestAwaiting removes the request-level waiting stamp.
func (s *Store) ClearRequestAwaiting(ctx context.Context, reqID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE requests SET awaiting_release_at = NULL
		WHERE id = ? AND awaiting_release_at IS NOT NULL`, reqID)
	return err
}
