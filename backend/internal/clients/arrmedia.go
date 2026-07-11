package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Media/queue/history surface of Sonarr (api/v3) and Radarr (api/v3).
// Prowlarr never calls these.

type Series struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	TvdbID     int64  `json:"tvdbId"`
	TitleSlug  string `json:"titleSlug"`
	Status     string `json:"status"`     // continuing | ended | upcoming
	NextAiring string `json:"nextAiring"` // ISO8601 of the next unaired monitored episode (empty if none)
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
	// Paginate: a large backfill queue easily exceeds one page, and an action
	// that misses a record would cancel a download whose client job keeps
	// running. Bounded at 20 pages (10k records) as a safety stop.
	var all []QueueRecord
	for page := 1; page <= 20; page++ {
		var out struct {
			Records      []QueueRecord `json:"records"`
			TotalRecords int           `json:"totalRecords"`
		}
		u := fmt.Sprintf("%s%s/queue?page=%d&pageSize=500&includeUnknownSeriesItems=true&includeUnknownMovieItems=true",
			c.BaseURL, c.APIBase, page)
		if _, err := getJSON(ctx, c.HTTP, u, c.headers(), &out); err != nil {
			return all, err
		}
		all = append(all, out.Records...)
		if len(out.Records) < 500 || len(all) >= out.TotalRecords {
			break
		}
	}
	return all, nil
}

// SetMovieMonitored flips a Radarr movie's monitored flag (used by Cancel to
// stop the arr re-downloading after we remove the queue item).
func (c *Arr) SetMovieMonitored(ctx context.Context, movieID int64, monitored bool) error {
	var movie map[string]any
	if _, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/movie/%d", c.BaseURL, c.APIBase, movieID), c.headers(), &movie); err != nil {
		return err
	}
	movie["monitored"] = monitored
	return c.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("%s%s/movie/%d", c.BaseURL, c.APIBase, movieID), movie)
}

// SetEpisodesMonitored flips the monitored flag on Sonarr episodes in bulk.
func (c *Arr) SetEpisodesMonitored(ctx context.Context, episodeIDs []int64, monitored bool) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	return c.doJSON(ctx, http.MethodPut, c.BaseURL+c.APIBase+"/episode/monitor",
		map[string]any{"episodeIds": episodeIDs, "monitored": monitored})
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

// DeleteQueueItem removes a queue record. removeFromClient cancels the job in
// the download client (arrarr, via the shim); blocklist bans the release so a
// re-search picks a different one.
func (c *Arr) DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error {
	u := fmt.Sprintf("%s%s/queue/%d?removeFromClient=%t&blocklist=%t",
		c.BaseURL, c.APIBase, id, removeFromClient, blocklist)
	return c.doDelete(ctx, u)
}

// Command posts an arr command (e.g. EpisodeSearch / MoviesSearch).
func (c *Arr) Command(ctx context.Context, name string, extra map[string]any) error {
	body := map[string]any{"name": name}
	for k, v := range extra {
		body[k] = v
	}
	return c.doJSON(ctx, http.MethodPost, c.BaseURL+c.APIBase+"/command", body)
}

// QueueRecordIDsForDownload returns the arr queue record ids sharing a
// downloadId (a season pack has one per episode).
func (c *Arr) QueueRecordIDsForDownload(ctx context.Context, downloadID string) ([]int64, error) {
	records, err := c.Queue(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range records {
		if strings.EqualFold(r.DownloadID, downloadID) {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

func (c *Arr) doDelete(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s delete: status %d", c.Name, resp.StatusCode)
	}
	return nil
}

func (c *Arr) doJSON(ctx context.Context, method, url string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: status %d", c.Name, method, resp.StatusCode)
	}
	return nil
}

var _ = url.QueryEscape
