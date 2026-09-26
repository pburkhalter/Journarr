package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Seerr is a thin Jellyseerr/Seerr v1 API client.
type Seerr struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewSeerr(baseURL, apiKey string, timeout time.Duration) *Seerr {
	return &Seerr{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

func (c *Seerr) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["X-Api-Key"] = c.APIKey
	}
	return h
}

// SeerrRequest is a tolerant subset of GET /api/v1/request results.
// status: 1=PENDING 2=APPROVED 3=DECLINED 4=FAILED 5=COMPLETED.
// media.status: 1=UNKNOWN 2=PENDING 3=PROCESSING 4=PARTIALLY_AVAILABLE 5=AVAILABLE.
type SeerrRequest struct {
	ID        int64  `json:"id"`
	Status    int    `json:"status"`
	Type      string `json:"type"` // movie|tv
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Media     struct {
		TmdbID int64 `json:"tmdbId"`
		TvdbID int64 `json:"tvdbId"`
		Status int   `json:"status"`
	} `json:"media"`
	Seasons []struct {
		SeasonNumber int64 `json:"seasonNumber"`
	} `json:"seasons"`
	RequestedBy struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	} `json:"requestedBy"`
}

// RecentRequests returns the most recently modified requests.
func (c *Seerr) RecentRequests(ctx context.Context, take int) ([]SeerrRequest, error) {
	var out struct {
		Results []SeerrRequest `json:"results"`
	}
	url := c.BaseURL + "/api/v1/request?take=" + itoa(take) + "&sort=modified&sortDirection=desc"
	if _, err := getJSON(ctx, c.HTTP, url, c.headers(), &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// MediaTitle resolves title/year/poster for a tmdb id (used when the poller
// sees a request the webhooks missed).
func (c *Seerr) MediaTitle(ctx context.Context, mediaType string, tmdbID int64) (title string, year int64, poster string, err error) {
	var body struct {
		Title        string `json:"title"`        // movie
		Name         string `json:"name"`         // tv
		ReleaseDate  string `json:"releaseDate"`  // movie
		FirstAirDate string `json:"firstAirDate"` // tv
		PosterPath   string `json:"posterPath"`
	}
	kind := "movie"
	if mediaType == "tv" {
		kind = "tv"
	}
	if _, err = getJSON(ctx, c.HTTP, c.BaseURL+"/api/v1/"+kind+"/"+itoa64(tmdbID), c.headers(), &body); err != nil {
		return "", 0, "", err
	}
	title = body.Title
	if title == "" {
		title = body.Name
	}
	date := body.ReleaseDate
	if date == "" {
		date = body.FirstAirDate
	}
	if len(date) >= 4 {
		for _, ch := range date[:4] {
			if ch < '0' || ch > '9' {
				goto done
			}
		}
		year = int64(date[0]-'0')*1000 + int64(date[1]-'0')*100 + int64(date[2]-'0')*10 + int64(date[3]-'0')
	}
done:
	if body.PosterPath != "" {
		poster = "https://image.tmdb.org/t/p/w600_and_h900_bestv2" + body.PosterPath
	}
	return title, year, poster, nil
}

func itoa(n int) string { return itoa64(int64(n)) }

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// MediaID returns Seerr's own media id for a title (0 when Seerr has no media
// entry for it, i.e. it was never requested or already removed).
func (c *Seerr) MediaID(ctx context.Context, mediaType string, tmdbID int64) (int64, error) {
	kind := "movie"
	if mediaType == "tv" {
		kind = "tv"
	}
	var body struct {
		MediaInfo *struct {
			ID int64 `json:"id"`
		} `json:"mediaInfo"`
	}
	if _, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v1/"+kind+"/"+itoa64(tmdbID), c.headers(), &body); err != nil {
		if isNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	if body.MediaInfo == nil {
		return 0, nil
	}
	return body.MediaInfo.ID, nil
}

// DeleteMedia removes Seerr's media entry with all its requests, so the title
// shows as not requested again. Seerr answers 204 also for an unknown id.
func (c *Seerr) DeleteMedia(ctx context.Context, mediaID int64) error {
	return c.delete(ctx, c.BaseURL+"/api/v1/media/"+itoa64(mediaID))
}

func (c *Seerr) delete(ctx context.Context, url string) error {
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
	// 404 = already gone; treat as success.
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return &APIError{Status: resp.StatusCode}
	}
	return nil
}

// DeleteRequest removes a Seerr request (used by the cancel action).
func (c *Seerr) DeleteRequest(ctx context.Context, id int64) error {
	return c.delete(ctx, c.BaseURL+"/api/v1/request/"+itoa64(id))
}

func (c *Seerr) CheckHealth(ctx context.Context) HealthResult {
	var body struct {
		Version       string `json:"version"`
		UpdateAvail   bool   `json:"updateAvailable"`
		RestartNeeded bool   `json:"restartRequired"`
	}
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v1/status", c.headers(), &body)
	if err != nil {
		return down(lat, err)
	}
	detail := map[string]any{}
	status := "up"
	if body.RestartNeeded {
		status = "degraded"
		detail["restart_required"] = true
	}
	return HealthResult{Status: status, Latency: lat, Version: body.Version, Detail: detail}
}
