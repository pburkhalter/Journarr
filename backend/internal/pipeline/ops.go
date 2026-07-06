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

// JobTransitionOp: an Arrarr job changed state (TorBox substages). Correlates
// to a download by nzo_id (usenet/SAB) or nzb_sha256 (torrent infohash).
type JobTransitionOp struct {
	NzoID           string `json:"nzo_id"`
	NzbSHA256       string `json:"nzb_sha256,omitempty"`
	Source          string `json:"source,omitempty"` // usenet|torrent
	From            string `json:"from,omitempty"`
	To              string `json:"to"` // NEW|SUBMITTED|DOWNLOADING|COMPLETED_TORBOX|READY|FAILED|CANCELED
	Filename        string `json:"filename,omitempty"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	BytesDownloaded int64  `json:"bytes_downloaded,omitempty"`
	BytesTotal      int64  `json:"bytes_total,omitempty"`
	LocalPath       string `json:"local_path,omitempty"`
	LastError       string `json:"last_error,omitempty"`
}

// AvailableOp: the Jellyfin poller matched a library item to a tracked media
// item (matching is done in the poller; the projector just applies the stage).
type AvailableOp struct {
	MediaItemID    int64  `json:"media_item_id"`
	JellyfinItemID string `json:"jellyfin_item_id,omitempty"`
	Note           string `json:"note,omitempty"` // provenance, e.g. "jellyfin" or reconciled
}

// EpisodeNum identifies an episode within a series notification.
type EpisodeNum struct {
	Season  int64 `json:"season"`
	Episode int64 `json:"episode"`
}

// NotifiedOp: waha-concierge sent a WhatsApp notification. Concierge carries
// the series/movie TMDB id; episodes are matched via the request's tmdb_id
// (media items are keyed by tvdb, but requests hold both).
type NotifiedOp struct {
	MediaType string       `json:"media_type"` // movie|tv
	TmdbID    int64        `json:"tmdb_id"`
	Title     string       `json:"title,omitempty"`
	Episodes  []EpisodeNum `json:"episodes,omitempty"`
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
