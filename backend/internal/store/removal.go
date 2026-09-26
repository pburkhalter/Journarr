package store

import (
	"context"
	"fmt"
	"strings"
)

// Tombstone names what a removal destroyed, so the projector can ignore
// stragglers for it (see migration 0009).
type Tombstone struct {
	Title           string
	SonarrSeriesIDs []int64
	RadarrMovieIDs  []int64
	SeerrRequestIDs []int64
}

// RequestsForTitle returns every request for the same title: all cycles and
// duplicates (Seerr re-requests create a second request). tv matches on tvdb
// or tmdb, movies on tmdb.
func (s *Store) RequestsForTitle(ctx context.Context, mediaType string, tmdbID, tvdbID *int64) ([]*Request, error) {
	var conds []string
	args := []any{mediaType}
	if tmdbID != nil && *tmdbID != 0 {
		conds = append(conds, "tmdb_id = ?")
		args = append(args, *tmdbID)
	}
	if mediaType == "tv" && tvdbID != nil && *tvdbID != 0 {
		conds = append(conds, "tvdb_id = ?")
		args = append(args, *tvdbID)
	}
	if len(conds) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		reqSelect+` WHERE media_type = ? AND (`+strings.Join(conds, " OR ")+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Request
	for rows.Next() {
		r, err := scanRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ArrIDsForRequests returns the distinct Sonarr series and Radarr movie ids
// recorded on the requests' items.
func (s *Store) ArrIDsForRequests(ctx context.Context, reqIDs []int64) (series, movies []int64, err error) {
	if len(reqIDs) == 0 {
		return nil, nil, nil
	}
	in, args := inList(reqIDs)
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(sonarr_series_id, 0), COALESCE(radarr_movie_id, 0)
		FROM media_items WHERE request_id IN `+in, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	seenS, seenM := map[int64]bool{}, map[int64]bool{}
	for rows.Next() {
		var sid, mid int64
		if err := rows.Scan(&sid, &mid); err != nil {
			return nil, nil, err
		}
		if sid != 0 && !seenS[sid] {
			seenS[sid] = true
			series = append(series, sid)
		}
		if mid != 0 && !seenM[mid] {
			seenM[mid] = true
			movies = append(movies, mid)
		}
	}
	return series, movies, rows.Err()
}

// PurgeRequests deletes the requests with everything hanging off them —
// items, transitions, downloads used only by them, their events and pending
// flow tasks — and records the tombstone, in one transaction. The actions
// audit is kept.
func (s *Store) PurgeRequests(ctx context.Context, reqIDs []int64, t Tombstone) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	in, args := inList(append([]int64{0}, reqIDs...)) // never an empty IN ()
	items := `(SELECT id FROM media_items WHERE request_id IN ` + in + `)`
	// arguments are repeated per use of the list
	rep := func(n int) []any {
		var out []any
		for i := 0; i < n; i++ {
			out = append(out, args...)
		}
		return out
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM flow_tasks WHERE status IN ('pending','failed','dead') AND (
			(target_type = 'request' AND target_id IN ` + in + `) OR
			(target_type = 'media_item' AND target_id IN ` + items + `))`, rep(2)},
		{`DELETE FROM events WHERE request_id IN ` + in + ` OR media_item_id IN ` + items, rep(2)},
		// Downloads linked to these items only; a season pack shared with a
		// request that stays is kept.
		{`DELETE FROM downloads WHERE id IN (
			SELECT download_id FROM download_items WHERE media_item_id IN ` + items + `)
			AND id NOT IN (
			SELECT download_id FROM download_items WHERE media_item_id NOT IN ` + items + `)`, rep(2)},
		{`DELETE FROM requests WHERE id IN ` + in, rep(1)},
	}
	if len(reqIDs) == 0 {
		stmts = nil
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("purge: %w", err)
		}
	}
	ins := func(arr any, arrID any, seerrID any) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO removed_media (arr, arr_id, seerr_request_id, title) VALUES (?, ?, ?, ?)`,
			arr, arrID, seerrID, t.Title)
		return err
	}
	for _, id := range t.SonarrSeriesIDs {
		if err := ins("sonarr", id, nil); err != nil {
			return err
		}
	}
	for _, id := range t.RadarrMovieIDs {
		if err := ins("radarr", id, nil); err != nil {
			return err
		}
	}
	for _, id := range t.SeerrRequestIDs {
		if err := ins(nil, nil, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsArrRemoved reports whether a Sonarr series / Radarr movie was removed
// everywhere; the projector then ignores events for it.
func (s *Store) IsArrRemoved(ctx context.Context, arr string, arrID int64) bool {
	if arrID == 0 {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM removed_media WHERE arr = ? AND arr_id = ?`, arr, arrID).Scan(&n)
	return n > 0
}

// IsSeerrRequestRemoved reports whether a Seerr request belonged to a title
// that was removed everywhere.
func (s *Store) IsSeerrRequestRemoved(ctx context.Context, seerrID int64) bool {
	if seerrID == 0 {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM removed_media WHERE seerr_request_id = ?`, seerrID).Scan(&n)
	return n > 0
}

// ReapRemoved drops tombstones older than the given number of days.
func (s *Store) ReapRemoved(ctx context.Context, olderThanDays int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM removed_media WHERE removed_at < datetime('now', ?)`, "-"+itoa(olderThanDays)+" days")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func inList(ids []int64) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return "(" + strings.Join(ph, ",") + ")", args
}
