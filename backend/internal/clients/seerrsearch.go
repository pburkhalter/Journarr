package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Seerr's search, title details and request creation (the "front door" an
// LLM uses to add titles, so Journarr tracks them from "requested" on).

// SeerrMediaStatus names Seerr's media.status codes.
func SeerrMediaStatus(code int) string {
	switch code {
	case 0:
		return "not_requested"
	case 1:
		return "unknown"
	case 2:
		return "pending"
	case 3:
		return "processing"
	case 4:
		return "partially_available"
	case 5:
		return "available"
	case 6:
		return "blocklisted"
	case 7:
		return "deleted"
	default:
		return fmt.Sprintf("status_%d", code)
	}
}

// SeerrSearchResult is one movie or series from GET /api/v1/search.
type SeerrSearchResult struct {
	ID           int64  `json:"id"` // TMDB id
	MediaType    string `json:"mediaType"`
	Title        string `json:"title"` // movie
	Name         string `json:"name"`  // tv
	ReleaseDate  string `json:"releaseDate"`
	FirstAirDate string `json:"firstAirDate"`
	Overview     string `json:"overview"`
	MediaInfo    *struct {
		Status int `json:"status"`
	} `json:"mediaInfo,omitempty"`
}

// DisplayTitle is the movie title or series name.
func (r SeerrSearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

// Year is the first four digits of the release or first air date.
func (r SeerrSearchResult) Year() string {
	d := r.ReleaseDate
	if d == "" {
		d = r.FirstAirDate
	}
	if len(d) >= 4 {
		return d[:4]
	}
	return ""
}

// Search queries TMDB through Seerr. Persons are dropped.
func (c *Seerr) Search(ctx context.Context, query string, page int) (results []SeerrSearchResult, totalPages int, err error) {
	if page < 1 {
		page = 1
	}
	// Seerr rejects '+' for spaces and unescaped reserved characters.
	q := strings.ReplaceAll(url.QueryEscape(query), "+", "%20")
	var body struct {
		TotalPages int                 `json:"totalPages"`
		Results    []SeerrSearchResult `json:"results"`
	}
	u := fmt.Sprintf("%s/api/v1/search?query=%s&page=%d", c.BaseURL, q, page)
	if _, err := getJSON(ctx, c.HTTP, u, c.headers(), &body); err != nil {
		return nil, 0, err
	}
	for _, r := range body.Results {
		if r.MediaType == "movie" || r.MediaType == "tv" {
			results = append(results, r)
		}
	}
	return results, body.TotalPages, nil
}

// SeerrDetails is the subset of GET /api/v1/{movie|tv}/{tmdbId} worth showing.
type SeerrDetails struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	Name             string `json:"name"`
	ReleaseDate      string `json:"releaseDate"`
	FirstAirDate     string `json:"firstAirDate"`
	Overview         string `json:"overview"`
	Runtime          int64  `json:"runtime"`
	Status           string `json:"status"` // TMDB status (Released, Returning Series, …)
	NumberOfSeasons  int64  `json:"numberOfSeasons"`
	NumberOfEpisodes int64  `json:"numberOfEpisodes"`
	Genres           []struct {
		Name string `json:"name"`
	} `json:"genres"`
	ExternalIDs struct {
		TvdbID int64  `json:"tvdbId"`
		ImdbID string `json:"imdbId"`
	} `json:"externalIds"`
	Seasons []struct {
		SeasonNumber int64  `json:"seasonNumber"`
		EpisodeCount int64  `json:"episodeCount"`
		AirDate      string `json:"airDate"`
	} `json:"seasons"`
	MediaInfo *struct {
		ID      int64 `json:"id"`
		Status  int   `json:"status"`
		Seasons []struct {
			SeasonNumber int64 `json:"seasonNumber"`
			Status       int   `json:"status"`
		} `json:"seasons"`
		Requests []struct {
			ID          int64  `json:"id"`
			Status      int    `json:"status"`
			CreatedAt   string `json:"createdAt"`
			RequestedBy struct {
				DisplayName string `json:"displayName"`
			} `json:"requestedBy"`
		} `json:"requests"`
	} `json:"mediaInfo,omitempty"`
}

// Details fetches a movie or series by TMDB id.
func (c *Seerr) Details(ctx context.Context, mediaType string, tmdbID int64) (*SeerrDetails, error) {
	kind := "movie"
	if mediaType == "tv" {
		kind = "tv"
	}
	var d SeerrDetails
	if _, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v1/"+kind+"/"+itoa64(tmdbID), c.headers(), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// NewSeerrRequest is the body of POST /api/v1/request. Seasons is nil for a
// movie; for a series either a list of season numbers or AllSeasons.
type NewSeerrRequest struct {
	MediaType  string
	TmdbID     int64
	Seasons    []int64
	AllSeasons bool
	ProfileID  int64  // 0 = Seerr's default for the server
	RootFolder string // "" = Seerr's default
}

// SeerrCreated is Seerr's answer to a new request.
type SeerrCreated struct {
	ID     int64 `json:"id"`
	Status int   `json:"status"` // 1 pending approval, 2 approved, …
	Media  struct {
		ID     int64 `json:"id"`
		Status int   `json:"status"`
	} `json:"media"`
}

// CreateRequest files a request in Seerr as the API key's user (the admin,
// so it is approved and sent to Sonarr/Radarr right away).
func (c *Seerr) CreateRequest(ctx context.Context, r NewSeerrRequest) (*SeerrCreated, error) {
	body := map[string]any{"mediaType": r.MediaType, "mediaId": r.TmdbID}
	if r.MediaType == "tv" {
		if r.AllSeasons {
			body["seasons"] = "all"
		} else {
			body["seasons"] = r.Seasons
		}
	}
	if r.ProfileID != 0 {
		body["profileId"] = r.ProfileID
	}
	if r.RootFolder != "" {
		body["rootFolder"] = r.RootFolder
	}
	var out SeerrCreated
	if err := c.postJSON(ctx, "/api/v1/request", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// postJSON posts body and decodes the answer; error answers carry Seerr's
// own message ("Request for this media already exists").
func (c *Seerr) postJSON(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(buf))
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
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return fmt.Errorf("seerr: %s (status %d)", e.Message, resp.StatusCode)
		}
		return &APIError{Service: "seerr", Status: resp.StatusCode}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
