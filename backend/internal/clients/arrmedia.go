package clients

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// Media/queue/history surface of Sonarr (api/v3) and Radarr (api/v3).
// Prowlarr never calls these.

type Series struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	TvdbID    int64  `json:"tvdbId"`
	TitleSlug string `json:"titleSlug"`
}

type Episode struct {
	ID            int64  `json:"id"`
	SeriesID      int64  `json:"seriesId"`
	SeasonNumber  int64  `json:"seasonNumber"`
	EpisodeNumber int64  `json:"episodeNumber"`
	Title         string `json:"title"`
	AirDateUTC    string `json:"airDateUtc"`
	Monitored     bool   `json:"monitored"`
	HasFile       bool   `json:"hasFile"` // episode file already on disk / imported
}

type Movie struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	TmdbID  int64  `json:"tmdbId"`
	Year    int64  `json:"year"`
	HasFile bool   `json:"hasFile"` // movie file already on disk / imported
}

// MovieByID fetches a single Radarr movie (for hasFile reconciliation).
func (c *Arr) MovieByID(ctx context.Context, id int64) (*Movie, error) {
	var m Movie
	if _, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/movie/%d", c.BaseURL, c.APIBase, id), c.headers(), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SeriesByTvdbID returns nil when the series is not (yet) in Sonarr.
func (c *Arr) SeriesByTvdbID(ctx context.Context, tvdbID int64) (*Series, error) {
	var out []Series
	if _, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/series?tvdbId=%d", c.BaseURL, c.APIBase, tvdbID),
		c.headers(), &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func (c *Arr) EpisodesBySeries(ctx context.Context, seriesID int64) ([]Episode, error) {
	var out []Episode
	_, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/episode?seriesId=%d", c.BaseURL, c.APIBase, seriesID),
		c.headers(), &out)
	return out, err
}

// MovieByTmdbID returns nil when the movie is not (yet) in Radarr.
func (c *Arr) MovieByTmdbID(ctx context.Context, tmdbID int64) (*Movie, error) {
	var out []Movie
	if _, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/movie?tmdbId=%d", c.BaseURL, c.APIBase, tmdbID),
		c.headers(), &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// QueueRecord is the tolerant union of Sonarr and Radarr queue rows. Sonarr
// emits one row per episode (season packs share downloadId); Radarr one per
// movie.
type QueueRecord struct {
	ID                    int64   `json:"id"`
	DownloadID            string  `json:"downloadId"`
	Title                 string  `json:"title"`
	Size                  float64 `json:"size"`
	SizeLeft              float64 `json:"sizeleft"`
	Status                string  `json:"status"`
	TrackedDownloadStatus string  `json:"trackedDownloadStatus"` // ok|warning|error
	TrackedDownloadState  string  `json:"trackedDownloadState"`  // downloading|importPending|importBlocked|...
	ErrorMessage          string  `json:"errorMessage"`
	EpisodeID             int64   `json:"episodeId"`
	SeriesID              int64   `json:"seriesId"`
	MovieID               int64   `json:"movieId"`
}

func (c *Arr) Queue(ctx context.Context) ([]QueueRecord, error) {
	var out struct {
		Records []QueueRecord `json:"records"`
	}
	_, err := getJSON(ctx, c.HTTP,
		c.BaseURL+c.APIBase+"/queue?page=1&pageSize=200&includeUnknownSeriesItems=true&includeUnknownMovieItems=true",
		c.headers(), &out)
	return out.Records, err
}

// HistoryRecord is the tolerant union of Sonarr and Radarr history rows.
// includeSeries/includeEpisode (Sonarr) and includeMovie (Radarr) embed the
// full entities so the poller can build complete ops without extra calls.
type HistoryRecord struct {
	ID          int64             `json:"id"`
	EventType   string            `json:"eventType"` // grabbed|downloadFolderImported|downloadFailed|...
	SourceTitle string            `json:"sourceTitle"`
	DownloadID  string            `json:"downloadId"`
	Date        time.Time         `json:"date"`
	EpisodeID   int64             `json:"episodeId"`
	SeriesID    int64             `json:"seriesId"`
	MovieID     int64             `json:"movieId"`
	Episode     *Episode          `json:"episode,omitempty"`
	Series      *Series           `json:"series,omitempty"`
	Movie       *Movie            `json:"movie,omitempty"`
	Data        map[string]string `json:"data"` // importedPath, indexer, size, protocol, message ...
}

// HistorySince pages newest-first and returns records with id > sinceID,
// oldest first (ready to feed the projector in order). complete=false means
// the page budget was exhausted before reaching sinceID — records older than
// the returned window exist but were not fetched.
//
// The boundary check filters per record instead of hard-breaking: rows are
// date-sorted and equal timestamps have unspecified order, so a record with
// id > sinceID can appear after one with id <= sinceID on the same page.
func (c *Arr) HistorySince(ctx context.Context, sinceID int64, maxPages int) ([]HistoryRecord, bool, error) {
	include := "&includeSeries=true&includeEpisode=true"
	if c.Name == "radarr" {
		include = "&includeMovie=true"
	}
	var collected []HistoryRecord
	complete := false
	for page := 1; page <= maxPages; page++ {
		var out struct {
			Records []HistoryRecord `json:"records"`
		}
		u := fmt.Sprintf("%s%s/history?page=%d&pageSize=100&sortKey=date&sortDirection=descending%s",
			c.BaseURL, c.APIBase, page, include)
		if _, err := getJSON(ctx, c.HTTP, u, c.headers(), &out); err != nil {
			return nil, false, err
		}
		if len(out.Records) == 0 {
			complete = true
			break
		}
		reachedBoundary := false
		for _, r := range out.Records {
			if r.ID <= sinceID {
				reachedBoundary = true
				continue
			}
			collected = append(collected, r)
		}
		if reachedBoundary {
			complete = true
			break
		}
		if len(out.Records) < 100 {
			complete = true // last page of history overall
			break
		}
	}
	// reverse to oldest-first, then sort stability by id for equal dates
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	sort.SliceStable(collected, func(i, j int) bool { return collected[i].ID < collected[j].ID })
	return collected, complete, nil
}

// Escape helper for future query building.
var _ = url.QueryEscape
