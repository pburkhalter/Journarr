package store

import (
	"context"
	"database/sql"
	"time"
)

type Request struct {
	ID             int64      `json:"id"`
	SeerrRequestID *int64     `json:"seerr_request_id,omitempty"`
	MediaType      string     `json:"media_type"`
	TmdbID         *int64     `json:"tmdb_id,omitempty"`
	TvdbID         *int64     `json:"tvdb_id,omitempty"`
	Title          string     `json:"title"`
	Year           *int64     `json:"year,omitempty"`
	PosterURL      string     `json:"poster_url,omitempty"`
	RequestedBy    string     `json:"requested_by,omitempty"`
	RequestedAt    *time.Time `json:"requested_at,omitempty"`
	Seasons        string     `json:"seasons,omitempty"` // JSON array
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// UpsertRequest inserts or refreshes a request identified by seerr_request_id.
// Fields that are empty/nil on the update path keep their existing values.
func (s *Store) UpsertRequest(ctx context.Context, r Request) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO requests (seerr_request_id, media_type, tmdb_id, tvdb_id, title, year,
			poster_url, requested_by, requested_at, seasons)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(seerr_request_id) DO UPDATE SET
			tmdb_id = COALESCE(excluded.tmdb_id, tmdb_id),
			tvdb_id = COALESCE(excluded.tvdb_id, tvdb_id),
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE title END,
			year = COALESCE(excluded.year, year),
			poster_url = CASE WHEN excluded.poster_url != '' THEN excluded.poster_url ELSE poster_url END,
			requested_by = CASE WHEN excluded.requested_by != '' THEN excluded.requested_by ELSE requested_by END,
			seasons = CASE WHEN excluded.seasons != '' THEN excluded.seasons ELSE seasons END,
			updated_at = CURRENT_TIMESTAMP`,
		r.SeerrRequestID, r.MediaType, r.TmdbID, r.TvdbID, r.Title, r.Year,
		r.PosterURL, r.RequestedBy, r.RequestedAt, r.Seasons)
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		if n, _ := res.RowsAffected(); n > 0 {
			// On conflict-update LastInsertId is unreliable; resolve by key.
			if r.SeerrRequestID != nil {
				var got int64
				if err := s.db.QueryRowContext(ctx,
					`SELECT id FROM requests WHERE seerr_request_id = ?`, *r.SeerrRequestID).Scan(&got); err == nil {
					return got, nil
				}
			}
			return id, nil
		}
	}
	if r.SeerrRequestID != nil {
		var got int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM requests WHERE seerr_request_id = ?`, *r.SeerrRequestID).Scan(&got)
		return got, err
	}
	return 0, sql.ErrNoRows
}

// InsertOrphanRequest creates a request row for arr activity that has no
// Seerr request (manual grabs, upgrades outside a request).
func (s *Store) InsertOrphanRequest(ctx context.Context, mediaType, title string, tmdbID, tvdbID *int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO requests (media_type, title, tmdb_id, tvdb_id, requested_by, requested_at)
		VALUES (?, ?, ?, ?, '', CURRENT_TIMESTAMP)`, mediaType, title, tmdbID, tvdbID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FindRequestBySeerrID(ctx context.Context, seerrID int64) (*Request, error) {
	return s.scanRequest(s.db.QueryRowContext(ctx, reqSelect+` WHERE seerr_request_id = ?`, seerrID))
}

func (s *Store) FindActiveRequestByTvdb(ctx context.Context, tvdbID int64) (*Request, error) {
	return s.scanRequest(s.db.QueryRowContext(ctx,
		reqSelect+` WHERE tvdb_id = ? AND status = 'active' ORDER BY id DESC LIMIT 1`, tvdbID))
}

func (s *Store) FindActiveRequestByTmdb(ctx context.Context, tmdbID int64, mediaType string) (*Request, error) {
	return s.scanRequest(s.db.QueryRowContext(ctx,
		reqSelect+` WHERE tmdb_id = ? AND media_type = ? AND status = 'active' ORDER BY id DESC LIMIT 1`, tmdbID, mediaType))
}

// FindRequestByTmdb finds the newest request for a tmdb id regardless of
// status (a notification can arrive after the request already completed).
func (s *Store) FindRequestByTmdb(ctx context.Context, tmdbID int64, mediaType string) (*Request, error) {
	return s.scanRequest(s.db.QueryRowContext(ctx,
		reqSelect+` WHERE tmdb_id = ? AND media_type = ? ORDER BY id DESC LIMIT 1`, tmdbID, mediaType))
}

func (s *Store) GetRequest(ctx context.Context, id int64) (*Request, error) {
	return s.scanRequest(s.db.QueryRowContext(ctx, reqSelect+` WHERE id = ?`, id))
}

func (s *Store) SetRequestStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE requests SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// RecomputeRequestStatus derives the rollup status from item stages:
// completed when every item reached 'available' or beyond; stays active
// otherwise. Canceled/failed are set explicitly, never derived.
func (s *Store) RecomputeRequestStatus(ctx context.Context, id int64) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM requests WHERE id = ?`, id).Scan(&status)
	if err != nil {
		return "", err
	}
	if status == "canceled" || status == "failed" {
		return status, nil
	}
	var total, done int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN (SELECT ordinal FROM stages WHERE key = current_stage) >=
				(SELECT ordinal FROM stages WHERE key = 'available') THEN 1 ELSE 0 END), 0)
		FROM media_items WHERE request_id = ?`, id).Scan(&total, &done)
	if err != nil {
		return "", err
	}
	if total == 0 {
		// No items to derive from (e.g. tv request closed out directly) —
		// never override an explicitly set status.
		return status, nil
	}
	next := "active"
	if done == total {
		next = "completed"
	}
	if next != status {
		if err := s.SetRequestStatus(ctx, id, next); err != nil {
			return "", err
		}
	}
	return next, nil
}

// RequestRollup is the flow-board summary of one request.
type RequestRollup struct {
	Request
	ItemCount         int            `json:"item_count"`
	StageCounts       map[string]int `json:"stage_counts"`
	LastError         string         `json:"last_error,omitempty"`
	StuckCount        int            `json:"stuck_count"`
	AwaitingReleaseAt *time.Time     `json:"awaiting_release_at,omitempty"`
}

func (s *Store) ListRequests(ctx context.Context, status, q string, limit, offset int) ([]RequestRollup, error) {
	query := reqSelect + ` WHERE 1=1`
	args := []any{}
	// A request is "waiting" if it (or one of its items) carries a future
	// release date. Waiting requests surface ONLY in the Waiting view — excluded
	// from Active and Done so they don't clutter what's actionable/finished.
	awaiting := `(requests.awaiting_release_at IS NOT NULL OR EXISTS (
		SELECT 1 FROM media_items mi WHERE mi.request_id = requests.id
		AND mi.awaiting_release_at IS NOT NULL))`
	switch status {
	case "", "all":
	case "waiting":
		query += ` AND ` + awaiting
	case "done":
		query += ` AND status IN ('completed','canceled','failed') AND NOT ` + awaiting
	default:
		query += ` AND status = ? AND NOT ` + awaiting
		args = append(args, status)
	}
	if q != "" {
		query += ` AND title LIKE ?`
		args = append(args, "%"+q+"%")
	}
	query += ` ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestRollup{}
	for rows.Next() {
		r, err := scanRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, RequestRollup{Request: *r})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		counts, itemCount, lastErr, stuck, awaiting, err := s.stageCounts(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].StageCounts = counts
		out[i].ItemCount = itemCount
		out[i].LastError = lastErr
		out[i].StuckCount = stuck
		out[i].AwaitingReleaseAt = awaiting
	}
	return out, nil
}

// RollupForRequest builds the same summary shape the list endpoint uses,
// for a single request (detail endpoint).
func (s *Store) RollupForRequest(ctx context.Context, id int64) (*RequestRollup, error) {
	r, err := s.GetRequest(ctx, id)
	if err != nil || r == nil {
		return nil, err
	}
	counts, total, lastErr, stuck, awaiting, err := s.stageCounts(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RequestRollup{Request: *r, StageCounts: counts, ItemCount: total, LastError: lastErr, StuckCount: stuck, AwaitingReleaseAt: awaiting}, nil
}

// ActiveTVRequestsWithoutItems finds approved Seerr tv requests whose fan-out
// never produced items (Sonarr down, restart during retry) — the projector
// sweep re-drives them.
func (s *Store) ActiveTVRequestsWithoutItems(ctx context.Context) ([]Request, error) {
	rows, err := s.db.QueryContext(ctx, reqSelect+`
		WHERE media_type = 'tv' AND status = 'active' AND seerr_request_id IS NOT NULL
		AND tvdb_id IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM media_items mi WHERE mi.request_id = requests.id)`)
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

func (s *Store) stageCounts(ctx context.Context, requestID int64) (counts map[string]int, total int, lastError string, stuck int, awaiting *time.Time, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT current_stage, COUNT(*) FROM media_items WHERE request_id = ? GROUP BY current_stage`, requestID)
	if err != nil {
		return nil, 0, "", 0, nil, err
	}
	defer rows.Close()
	counts = map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, 0, "", 0, nil, err
		}
		counts[k] = n
		total += n
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", 0, nil, err
	}
	var lastErr sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT last_error FROM media_items
		WHERE request_id = ? AND last_error IS NOT NULL AND last_error != ''
		ORDER BY updated_at DESC LIMIT 1`, requestID).Scan(&lastErr)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_items WHERE request_id = ? AND stuck_since IS NOT NULL`, requestID).Scan(&stuck)
	// Waiting-for-release date: the request-level stamp (tv next-airing) wins,
	// else the soonest awaiting item (movies). Scanned as a string, not
	// sql.NullTime — this is a COALESCE/MIN expression, so the driver returns a
	// raw string (see parseSQLiteTime).
	var awaitStr sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(
			(SELECT awaiting_release_at FROM requests WHERE id = ?),
			(SELECT MIN(awaiting_release_at) FROM media_items
				WHERE request_id = ? AND awaiting_release_at IS NOT NULL))`,
		requestID, requestID).Scan(&awaitStr)
	if awaitStr.Valid {
		if t, ok := parseSQLiteTime(awaitStr.String); ok {
			awaiting = &t
		}
	}
	return counts, total, lastErr.String, stuck, awaiting, nil
}

const reqSelect = `SELECT id, seerr_request_id, media_type, tmdb_id, tvdb_id, title, year,
	COALESCE(poster_url,''), COALESCE(requested_by,''), requested_at, COALESCE(seasons,''),
	status, created_at, updated_at FROM requests`

type rowScanner interface{ Scan(dest ...any) error }

func (s *Store) scanRequest(row *sql.Row) (*Request, error) {
	r, err := scanRequestRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func scanRequestRow(row rowScanner) (*Request, error) {
	var r Request
	var reqAt sql.NullTime
	var created, updated sql.NullTime
	if err := row.Scan(&r.ID, &r.SeerrRequestID, &r.MediaType, &r.TmdbID, &r.TvdbID,
		&r.Title, &r.Year, &r.PosterURL, &r.RequestedBy, &reqAt, &r.Seasons,
		&r.Status, &created, &updated); err != nil {
		return nil, err
	}
	if reqAt.Valid {
		r.RequestedAt = &reqAt.Time
	}
	r.CreatedAt = created.Time
	r.UpdatedAt = updated.Time
	return &r, nil
}
