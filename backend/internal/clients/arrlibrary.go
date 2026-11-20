package clients

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Library surface of Sonarr/Radarr (api/v3): the whole collection, quality
// profiles, the calendar and edits. Used by the MCP server; the pipeline
// itself only needs the narrower types in arrmedia.go.

// QualityProfile is one Sonarr/Radarr quality profile.
type QualityProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// QualityProfiles lists the arr's quality profiles.
func (c *Arr) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	var out []QualityProfile
	_, err := getJSON(ctx, c.HTTP, c.BaseURL+c.APIBase+"/qualityprofile", c.headers(), &out)
	return out, err
}

// LibraryMovie is a Radarr movie with the fields worth showing or editing.
type LibraryMovie struct {
	ID                  int64  `json:"id"`
	Title               string `json:"title"`
	Year                int64  `json:"year"`
	TmdbID              int64  `json:"tmdbId"`
	ImdbID              string `json:"imdbId"`
	Monitored           bool   `json:"monitored"`
	HasFile             bool   `json:"hasFile"`
	IsAvailable         bool   `json:"isAvailable"`
	SizeOnDisk          int64  `json:"sizeOnDisk"`
	QualityProfileID    int64  `json:"qualityProfileId"`
	MinimumAvailability string `json:"minimumAvailability"` // announced|inCinemas|released
	Status              string `json:"status"`              // tba|announced|inCinemas|released|deleted
	Path                string `json:"path"`
	Added               string `json:"added"`
	InCinemas           string `json:"inCinemas"`
	DigitalRelease      string `json:"digitalRelease"`
	PhysicalRelease     string `json:"physicalRelease"`
	Runtime             int64  `json:"runtime"`
	MovieFile           *struct {
		RelativePath string `json:"relativePath"`
		Quality      struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
		} `json:"quality"`
	} `json:"movieFile,omitempty"`
}

// SeasonStats are Sonarr's per-season and per-series counters.
type SeasonStats struct {
	EpisodeFileCount  int64   `json:"episodeFileCount"`
	EpisodeCount      int64   `json:"episodeCount"` // aired (or monitored) episodes
	TotalEpisodeCount int64   `json:"totalEpisodeCount"`
	SizeOnDisk        int64   `json:"sizeOnDisk"`
	PercentOfEpisodes float64 `json:"percentOfEpisodes"`
	SeasonCount       int64   `json:"seasonCount,omitempty"`
}

// LibrarySeries is a Sonarr series with its seasons.
type LibrarySeries struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	Year             int64  `json:"year"`
	TvdbID           int64  `json:"tvdbId"`
	TmdbID           int64  `json:"tmdbId"`
	Monitored        bool   `json:"monitored"`
	QualityProfileID int64  `json:"qualityProfileId"`
	Status           string `json:"status"` // continuing|ended|upcoming|deleted
	Path             string `json:"path"`
	Added            string `json:"added"`
	NextAiring       string `json:"nextAiring"`
	PreviousAiring   string `json:"previousAiring"`
	Seasons          []struct {
		SeasonNumber int64       `json:"seasonNumber"`
		Monitored    bool        `json:"monitored"`
		Statistics   SeasonStats `json:"statistics"`
	} `json:"seasons"`
	Statistics SeasonStats `json:"statistics"`
}

// Movies returns Radarr's whole collection.
func (c *Arr) Movies(ctx context.Context) ([]LibraryMovie, error) {
	var out []LibraryMovie
	_, err := getJSON(ctx, c.HTTP, c.BaseURL+c.APIBase+"/movie", c.headers(), &out)
	return out, err
}

// LibraryMovieByID fetches one Radarr movie.
func (c *Arr) LibraryMovieByID(ctx context.Context, id int64) (*LibraryMovie, error) {
	var m LibraryMovie
	if _, err := getJSON(ctx, c.HTTP, fmt.Sprintf("%s%s/movie/%d", c.BaseURL, c.APIBase, id), c.headers(), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// AllSeries returns Sonarr's whole collection.
func (c *Arr) AllSeries(ctx context.Context) ([]LibrarySeries, error) {
	var out []LibrarySeries
	_, err := getJSON(ctx, c.HTTP, c.BaseURL+c.APIBase+"/series", c.headers(), &out)
	return out, err
}

// LibrarySeriesByID fetches one Sonarr series.
func (c *Arr) LibrarySeriesByID(ctx context.Context, id int64) (*LibrarySeries, error) {
	var s LibrarySeries
	if _, err := getJSON(ctx, c.HTTP, fmt.Sprintf("%s%s/series/%d", c.BaseURL, c.APIBase, id), c.headers(), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateEntity edits a movie or series ("movie"|"series") by fetching it,
// letting edit change the raw JSON object and putting it back — Sonarr and
// Radarr want the full resource on PUT, and a round trip through a typed
// struct would drop the fields it does not know.
func (c *Arr) UpdateEntity(ctx context.Context, resource string, id int64, edit func(map[string]any) error) error {
	u := fmt.Sprintf("%s%s/%s/%d", c.BaseURL, c.APIBase, resource, id)
	var obj map[string]any
	if _, err := getJSON(ctx, c.HTTP, u, c.headers(), &obj); err != nil {
		return err
	}
	if err := edit(obj); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPut, u, obj)
}

// CalendarEpisode is one Sonarr calendar row (includeSeries=true).
type CalendarEpisode struct {
	SeriesID      int64  `json:"seriesId"`
	SeasonNumber  int64  `json:"seasonNumber"`
	EpisodeNumber int64  `json:"episodeNumber"`
	Title         string `json:"title"`
	AirDateUTC    string `json:"airDateUtc"`
	HasFile       bool   `json:"hasFile"`
	Monitored     bool   `json:"monitored"`
	Series        struct {
		Title  string `json:"title"`
		TvdbID int64  `json:"tvdbId"`
	} `json:"series"`
}

// EpisodeCalendar lists Sonarr episodes airing between start and end.
func (c *Arr) EpisodeCalendar(ctx context.Context, start, end time.Time) ([]CalendarEpisode, error) {
	var out []CalendarEpisode
	_, err := getJSON(ctx, c.HTTP, c.calendarURL(start, end)+"&includeSeries=true", c.headers(), &out)
	return out, err
}

// MovieCalendar lists Radarr movies with a cinema, digital or physical
// release between start and end.
func (c *Arr) MovieCalendar(ctx context.Context, start, end time.Time) ([]LibraryMovie, error) {
	var out []LibraryMovie
	_, err := getJSON(ctx, c.HTTP, c.calendarURL(start, end), c.headers(), &out)
	return out, err
}

func (c *Arr) calendarURL(start, end time.Time) string {
	q := url.Values{}
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	q.Set("unmonitored", "true")
	return c.BaseURL + c.APIBase + "/calendar?" + q.Encode()
}
