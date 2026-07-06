// Package pipeline is Journarr's core: it folds raw events (webhooks +
// poller observations) into per-item stage timelines. All writes to the
// derived tables happen in the single projector goroutine.
package pipeline

// Normalized operations. Webhook payloads and poller records are both
// translated into these before touching the database, so every source
// converges on the same idempotent apply-functions.

type SeriesRef struct {
	SonarrID int64  `json:"sonarr_id"`
	TvdbID   int64  `json:"tvdb_id"`
	TmdbID   int64  `json:"tmdb_id"`
	Title    string `json:"title"`
	Poster   string `json:"poster,omitempty"`
}

type EpisodeRef struct {
	SonarrID int64  `json:"sonarr_id"`
	Season   int64  `json:"season"`
	Episode  int64  `json:"episode"`
	Title    string `json:"title,omitempty"`
}

type MovieRef struct {
	RadarrID int64  `json:"radarr_id"`
	TmdbID   int64  `json:"tmdb_id"`
	Title    string `json:"title"`
	Year     int64  `json:"year,omitempty"`
	Poster   string `json:"poster,omitempty"`
}

// GrabOp: a release was sent to the download client.
type GrabOp struct {
	Arr          string       `json:"arr"` // sonarr|radarr
	DownloadID   string       `json:"download_id"`
	ReleaseTitle string       `json:"release_title,omitempty"`
	Indexer      string       `json:"indexer,omitempty"`
	Size         int64        `json:"size,omitempty"`
	Protocol     string       `json:"protocol,omitempty"` // usenet|torrent
	Series       *SeriesRef   `json:"series,omitempty"`
	Episodes     []EpisodeRef `json:"episodes,omitempty"`
	Movie        *MovieRef    `json:"movie,omitempty"`
}

// ImportOp: the arr moved files into the library.
type ImportOp struct {
	Arr        string       `json:"arr"`
	DownloadID string       `json:"download_id,omitempty"`
	Series     *SeriesRef   `json:"series,omitempty"`
	Episodes   []EpisodeRef `json:"episodes,omitempty"`
	Movie      *MovieRef    `json:"movie,omitempty"`
	// EpisodePaths maps sonarr episode id -> imported file path.
	EpisodePaths map[int64]string `json:"episode_paths,omitempty"`
	MoviePath    string           `json:"movie_path,omitempty"`
	IsUpgrade    bool             `json:"is_upgrade,omitempty"`
}

// FailureOp: download failed (history-only — the arrs have no failure
// webhooks). Soft failures (ManualInteractionRequired) annotate the items
// but leave the download state untouched — a human may still rescue it.
type FailureOp struct {
	Arr        string       `json:"arr"`
	DownloadID string       `json:"download_id,omitempty"`
	Message    string       `json:"message"`
	Soft       bool         `json:"soft,omitempty"`
	Series     *SeriesRef   `json:"series,omitempty"`
	Episodes   []EpisodeRef `json:"episodes,omitempty"`
	Movie      *MovieRef    `json:"movie,omitempty"`
}

// SeerrOp: request lifecycle signal, from webhook or the request poller.
type SeerrOp struct {
	SeerrRequestID int64   `json:"seerr_request_id"`
	Kind           string  `json:"kind"` // pending|approved|declined|available|failed
	MediaType      string  `json:"media_type"` // movie|tv
	TmdbID         int64   `json:"tmdb_id,omitempty"`
	TvdbID         int64   `json:"tvdb_id,omitempty"`
	Title          string  `json:"title,omitempty"`
	Year           int64   `json:"year,omitempty"`
	Poster         string  `json:"poster,omitempty"`
	RequestedBy    string  `json:"requested_by,omitempty"`
	Seasons        []int64 `json:"seasons,omitempty"`
}
