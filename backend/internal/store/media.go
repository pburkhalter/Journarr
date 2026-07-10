package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type MediaItem struct {
	ID              int64      `json:"id"`
	RequestID       *int64     `json:"request_id,omitempty"`
	MediaType       string     `json:"media_type"` // movie|episode
	TmdbID          *int64     `json:"tmdb_id,omitempty"`
	TvdbID          *int64     `json:"tvdb_id,omitempty"`
	SonarrSeriesID  *int64     `json:"sonarr_series_id,omitempty"`
	SonarrEpisodeID *int64     `json:"sonarr_episode_id,omitempty"`
	RadarrMovieID   *int64     `json:"radarr_movie_id,omitempty"`
	SeasonNumber    *int64     `json:"season_number,omitempty"`
	EpisodeNumber   *int64     `json:"episode_number,omitempty"`
	Title           string     `json:"title"`
	CurrentStage    string     `json:"current_stage"`
	CurrentCycle    int        `json:"current_cycle"`
	StuckSince      *time.Time `json:"stuck_since,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ImportedPath    string     `json:"imported_path,omitempty"`
	JellyfinItemID  string     `json:"jellyfin_item_id,omitempty"`
	AwaitingReleaseAt *time.Time `json:"awaiting_release_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const itemSelect = `SELECT id, request_id, media_type, tmdb_id, tvdb_id, sonarr_series_id,
	sonarr_episode_id, radarr_movie_id, season_number, episode_number, title,
	current_stage, current_cycle, stuck_since, COALESCE(last_error,''),
	COALESCE(imported_path,''), COALESCE(jellyfin_item_id,''), awaiting_release_at, updated_at FROM media_items`

func scanItem(row rowScanner) (*MediaItem, error) {
	var m MediaItem
	var stuck sql.NullTime
	var awaiting sql.NullTime
	var updated sql.NullTime
	var season, episode int64
	if err := row.Scan(&m.ID, &m.RequestID, &m.MediaType, &m.TmdbID, &m.TvdbID,
		&m.SonarrSeriesID, &m.SonarrEpisodeID, &m.RadarrMovieID,
		&season, &episode, &m.Title,
		&m.CurrentStage, &m.CurrentCycle, &stuck, &m.LastError,
		&m.ImportedPath, &m.JellyfinItemID, &awaiting, &updated); err != nil {
		return nil, err
	}
	if awaiting.Valid {
		m.AwaitingReleaseAt = &awaiting.Time
	}
	if season >= 0 {
		m.SeasonNumber = &season
	}
	if episode >= 0 {
		m.EpisodeNumber = &episode
	}
	if stuck.Valid {
		m.StuckSince = &stuck.Time
	}
	m.UpdatedAt = updated.Time
	return &m, nil
}

// seNum maps optional season/episode numbers onto the NOT NULL sentinel -1
// (SQLite UNIQUE treats NULLs as distinct, which would break the upsert for
// movies).
func seNum(v *int64) int64 {
	if v == nil {
		return -1
	}
	return *v
}

// EnsureMediaItem inserts an item if it doesn't exist yet (idempotent on the
// request/type/season/episode unique key) and returns its id.
func (s *Store) EnsureMediaItem(ctx context.Context, m MediaItem) (int64, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_items (request_id, media_type, tmdb_id, tvdb_id, sonarr_series_id,
			sonarr_episode_id, radarr_movie_id, season_number, episode_number, title, current_stage)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id, media_type, season_number, episode_number) DO UPDATE SET
			sonarr_series_id = COALESCE(excluded.sonarr_series_id, sonarr_series_id),
			sonarr_episode_id = COALESCE(excluded.sonarr_episode_id, sonarr_episode_id),
			radarr_movie_id = COALESCE(excluded.radarr_movie_id, radarr_movie_id),
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE title END,
			updated_at = CURRENT_TIMESTAMP`,
		m.RequestID, m.MediaType, m.TmdbID, m.TvdbID, m.SonarrSeriesID,
		m.SonarrEpisodeID, m.RadarrMovieID, seNum(m.SeasonNumber), seNum(m.EpisodeNumber),
		m.Title, coalesceStage(m.CurrentStage))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM media_items WHERE request_id IS ? AND media_type = ?
		AND season_number = ? AND episode_number = ?`,
		m.RequestID, m.MediaType, seNum(m.SeasonNumber), seNum(m.EpisodeNumber)).Scan(&id)
	return id, err
}

func coalesceStage(s string) string {
	if s == "" {
		return "requested"
	}
	return s
}

func (s *Store) GetMediaItem(ctx context.Context, id int64) (*MediaItem, error) {
	m, err := scanItem(s.db.QueryRowContext(ctx, itemSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// FindItemsBySonarrEpisodeIDs maps Sonarr episode ids to items (most recent
// request first, so re-requests win over stale completed ones).
func (s *Store) FindItemsBySonarrEpisodeIDs(ctx context.Context, episodeIDs []int64) (map[int64]*MediaItem, error) {
	out := map[int64]*MediaItem{}
	for _, epID := range episodeIDs {
		m, err := scanItem(s.db.QueryRowContext(ctx,
			itemSelect+` WHERE sonarr_episode_id = ? ORDER BY id DESC LIMIT 1`, epID))
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[epID] = m
	}
	return out, nil
}

// FindItemByEpisodeNumbers falls back to tvdb + SxxEyy matching when the
// Sonarr episode id is not yet linked.
func (s *Store) FindItemByEpisodeNumbers(ctx context.Context, requestID int64, season, episode int64) (*MediaItem, error) {
	m, err := scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE request_id = ? AND media_type = 'episode'
		AND season_number = ? AND episode_number = ? LIMIT 1`, requestID, season, episode))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func (s *Store) FindMovieItemByRequest(ctx context.Context, requestID int64) (*MediaItem, error) {
	m, err := scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE request_id = ? AND media_type = 'movie' LIMIT 1`, requestID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// FindMovieItemByTmdb finds the newest movie item for a tmdb id (any request).
func (s *Store) FindMovieItemByTmdb(ctx context.Context, tmdbID int64) (*MediaItem, error) {
	m, err := scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE media_type = 'movie' AND tmdb_id = ? ORDER BY id DESC LIMIT 1`, tmdbID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// FindEpisodeItemByTvdb finds an episode item by series tvdb id + numbers.
func (s *Store) FindEpisodeItemByTvdb(ctx context.Context, tvdbID, season, episode int64) (*MediaItem, error) {
	m, err := scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE media_type = 'episode' AND tvdb_id = ? AND season_number = ? AND episode_number = ?
		ORDER BY id DESC LIMIT 1`, tvdbID, season, episode))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// FindItemByImportedPath resolves a media item by the file an arr imported.
// Tries an exact match first, then a basename match — robust to mount-prefix
// differences between Tdarr and the arrs (they may see the same file under
// different roots).
func (s *Store) FindItemByImportedPath(ctx context.Context, path string) (*MediaItem, error) {
	if path == "" {
		return nil, nil
	}
	m, err := scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE imported_path = ? ORDER BY id DESC LIMIT 1`, path))
	if err == nil {
		return m, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	base := path
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		base = path[i+1:]
	}
	if base == "" {
		return nil, nil
	}
	// Match on trailing basename; escape LIKE metacharacters (release names
	// often contain '_').
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(base)
	m, err = scanItem(s.db.QueryRowContext(ctx,
		itemSelect+` WHERE imported_path LIKE ? ESCAPE '\' ORDER BY id DESC LIMIT 1`, "%"+esc))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func (s *Store) SetItemJellyfinID(ctx context.Context, id int64, jellyfinID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media_items SET jellyfin_item_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, jellyfinID, id)
	return err
}

// ListUnavailableActiveItems returns tracked items in active requests that
// have not yet reached 'available' — the working set for the presence
// reconciler (which advances items whose file already exists in the arr).
func (s *Store) ListUnavailableActiveItems(ctx context.Context) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, itemSelect+`
		WHERE request_id IN (SELECT id FROM requests WHERE status = 'active')
		AND (SELECT ordinal FROM stages WHERE key = media_items.current_stage)
			< (SELECT ordinal FROM stages WHERE key = 'available')
		ORDER BY sonarr_series_id, radarr_movie_id, id`)
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

func (s *Store) ListItemsForRequest(ctx context.Context, requestID int64) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx,
		itemSelect+` WHERE request_id = ? ORDER BY season_number, episode_number, id`, requestID)
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

func (s *Store) SetItemError(ctx context.Context, id int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_items SET last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, msg, id)
	return err
}

func (s *Store) SetItemImportedPath(ctx context.Context, id int64, path string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_items SET imported_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, path, id)
	return err
}

// BumpItemCycle starts a new attempt (re-grab/upgrade): resets the stage
// pointer so the new cycle can advance from the start, clears the error and
// returns the new cycle number.
func (s *Store) BumpItemCycle(ctx context.Context, id int64) (int, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_items SET current_cycle = current_cycle + 1,
			current_stage = 'requested', last_error = '', stuck_since = NULL,
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	var cycle int
	err = s.db.QueryRowContext(ctx, `SELECT current_cycle FROM media_items WHERE id = ?`, id).Scan(&cycle)
	return cycle, err
}
