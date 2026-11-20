package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/pburkhalter/journarr/internal/clients"
)

// Title-level interventions beyond the pipeline: request a title through
// Seerr, edit a library entry in Sonarr/Radarr, trigger a search. Each is
// audited like the other actions.

// ErrNotInLibrary means Sonarr/Radarr has no entry for the title.
var ErrNotInLibrary = errors.New("title is not in Sonarr/Radarr")

// LibraryEntry is a title resolved to its Sonarr/Radarr entry.
type LibraryEntry struct {
	Title  Title
	Arr    *clients.Arr
	Kind   string // sonarr|radarr
	ArrID  int64
	Movie  *clients.LibraryMovie
	Series *clients.LibrarySeries
}

// ResolveLibrary finds the Sonarr series or Radarr movie of a title. A
// series given only by TMDB id gets its TVDB id from Seerr. Returns
// ErrNotInLibrary when the arr has no entry.
func (a *Actions) ResolveLibrary(ctx context.Context, t Title) (*LibraryEntry, error) {
	switch t.MediaType {
	case "movie":
		if a.Radarr == nil {
			return nil, errors.New("radarr is not configured")
		}
		if t.TmdbID == 0 {
			return nil, errors.New("a movie needs its tmdb_id")
		}
		mv, err := a.Radarr.MovieByTmdbID(ctx, t.TmdbID)
		if err != nil {
			return nil, fmt.Errorf("radarr: %w", err)
		}
		if mv == nil {
			return nil, ErrNotInLibrary
		}
		full, err := a.Radarr.LibraryMovieByID(ctx, mv.ID)
		if err != nil {
			return nil, fmt.Errorf("radarr: %w", err)
		}
		return &LibraryEntry{Title: t, Arr: a.Radarr, Kind: "radarr", ArrID: mv.ID, Movie: full}, nil
	case "tv":
		if a.Sonarr == nil {
			return nil, errors.New("sonarr is not configured")
		}
		if t.TvdbID == 0 && t.TmdbID != 0 && a.Seerr != nil {
			id, err := a.Seerr.TvdbID(ctx, t.TmdbID)
			if err != nil {
				return nil, fmt.Errorf("seerr: %w", err)
			}
			t.TvdbID = id
		}
		if t.TvdbID == 0 {
			return nil, errors.New("a series needs its tvdb_id (or a tmdb_id Seerr can map)")
		}
		sr, err := a.Sonarr.SeriesByTvdbID(ctx, t.TvdbID)
		if err != nil {
			return nil, fmt.Errorf("sonarr: %w", err)
		}
		if sr == nil {
			return nil, ErrNotInLibrary
		}
		full, err := a.Sonarr.LibrarySeriesByID(ctx, sr.ID)
		if err != nil {
			return nil, fmt.Errorf("sonarr: %w", err)
		}
		return &LibraryEntry{Title: t, Arr: a.Sonarr, Kind: "sonarr", ArrID: sr.ID, Series: full}, nil
	default:
		return nil, fmt.Errorf("media_type must be movie or tv, got %q", t.MediaType)
	}
}

// TitleRequest asks Seerr for a title.
type TitleRequest struct {
	MediaType        string  `json:"media_type"`
	TmdbID           int64   `json:"tmdb_id"`
	Seasons          []int64 `json:"seasons,omitempty"`
	AllSeasons       bool    `json:"all_seasons,omitempty"`
	QualityProfileID int64   `json:"quality_profile_id,omitempty"`
	RootFolder       string  `json:"root_folder,omitempty"`
}

// RequestTitle files a Seerr request. Going through Seerr (not straight into
// Radarr/Sonarr) means Journarr tracks the title from "requested" on and the
// completion notice goes out like for any other request.
func (a *Actions) RequestTitle(ctx context.Context, r TitleRequest) (*clients.SeerrCreated, error) {
	id := a.begin(ctx, "request_title", r.MediaType, r.TmdbID, r)
	if a.Seerr == nil {
		return nil, a.finish(ctx, id, errors.New("seerr is not configured"))
	}
	if r.MediaType != "movie" && r.MediaType != "tv" {
		return nil, a.finish(ctx, id, fmt.Errorf("media_type must be movie or tv, got %q", r.MediaType))
	}
	if r.TmdbID == 0 {
		return nil, a.finish(ctx, id, errors.New("tmdb_id is required"))
	}
	if r.MediaType == "tv" && !r.AllSeasons && len(r.Seasons) == 0 {
		return nil, a.finish(ctx, id, errors.New("a series request needs seasons or all_seasons"))
	}
	created, err := a.Seerr.CreateRequest(ctx, clients.NewSeerrRequest{
		MediaType: r.MediaType, TmdbID: r.TmdbID, Seasons: r.Seasons, AllSeasons: r.AllSeasons,
		ProfileID: r.QualityProfileID, RootFolder: r.RootFolder,
	})
	if err != nil {
		return nil, a.finish(ctx, id, err)
	}
	if a.Wake != nil {
		a.Wake()
	}
	return created, a.finishDetail(ctx, id, nil, fmt.Sprintf("seerr request %d", created.ID))
}

// SeasonMonitor sets one season's monitored flag.
type SeasonMonitor struct {
	SeasonNumber int64 `json:"season_number"`
	Monitored    bool  `json:"monitored"`
}

// TitleUpdate lists the library fields to change; nil means unchanged.
type TitleUpdate struct {
	Monitored           *bool           `json:"monitored,omitempty"`
	QualityProfileID    *int64          `json:"quality_profile_id,omitempty"`
	MinimumAvailability *string         `json:"minimum_availability,omitempty"` // movies
	Seasons             []SeasonMonitor `json:"seasons,omitempty"`              // series
}

var minimumAvailability = []string{"announced", "inCinemas", "released"}

// UpdateTitle edits a Sonarr series or Radarr movie. Changing a season's
// monitored flag makes Sonarr (un)monitor its episodes as well.
func (a *Actions) UpdateTitle(ctx context.Context, t Title, u TitleUpdate) (*LibraryEntry, error) {
	id := a.begin(ctx, "update_title", t.MediaType, firstNonZero(t.TmdbID, t.TvdbID), map[string]any{"title": t, "update": u})
	if u.Monitored == nil && u.QualityProfileID == nil && u.MinimumAvailability == nil && len(u.Seasons) == 0 {
		return nil, a.finish(ctx, id, errors.New("nothing to change"))
	}
	e, err := a.ResolveLibrary(ctx, t)
	if err != nil {
		return nil, a.finish(ctx, id, err)
	}
	if u.QualityProfileID != nil {
		profiles, err := e.Arr.QualityProfiles(ctx)
		if err != nil {
			return nil, a.finish(ctx, id, err)
		}
		if !slices.ContainsFunc(profiles, func(p clients.QualityProfile) bool { return p.ID == *u.QualityProfileID }) {
			return nil, a.finish(ctx, id, fmt.Errorf("quality profile %d does not exist in %s", *u.QualityProfileID, e.Kind))
		}
	}
	resource := "movie"
	if e.Kind == "sonarr" {
		resource = "series"
		if u.MinimumAvailability != nil {
			return nil, a.finish(ctx, id, errors.New("minimum_availability applies to movies only"))
		}
	} else if len(u.Seasons) > 0 {
		return nil, a.finish(ctx, id, errors.New("seasons apply to series only"))
	}
	if u.MinimumAvailability != nil && !slices.Contains(minimumAvailability, *u.MinimumAvailability) {
		return nil, a.finish(ctx, id, fmt.Errorf("minimum_availability must be one of %v", minimumAvailability))
	}
	err = e.Arr.UpdateEntity(ctx, resource, e.ArrID, func(obj map[string]any) error {
		if u.Monitored != nil {
			obj["monitored"] = *u.Monitored
		}
		if u.QualityProfileID != nil {
			obj["qualityProfileId"] = *u.QualityProfileID
		}
		if u.MinimumAvailability != nil {
			obj["minimumAvailability"] = *u.MinimumAvailability
		}
		return setSeasonMonitoring(obj, u.Seasons)
	})
	if err != nil {
		return nil, a.finish(ctx, id, err)
	}
	fresh, err := a.ResolveLibrary(ctx, e.Title)
	if err != nil {
		fresh = e
	}
	return fresh, a.finish(ctx, id, nil)
}

// setSeasonMonitoring flips the monitored flag of the listed seasons in a raw
// Sonarr series object; an unknown season number is an error.
func setSeasonMonitoring(obj map[string]any, want []SeasonMonitor) error {
	if len(want) == 0 {
		return nil
	}
	seasons, _ := obj["seasons"].([]any)
	for _, w := range want {
		found := false
		for _, raw := range seasons {
			s, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if n, ok := s["seasonNumber"].(float64); ok && int64(n) == w.SeasonNumber {
				s["monitored"] = w.Monitored
				found = true
			}
		}
		if !found {
			return fmt.Errorf("season %d does not exist", w.SeasonNumber)
		}
	}
	return nil
}

// SearchTitle makes Sonarr/Radarr search the indexers now: the movie, the
// whole series, or one season.
func (a *Actions) SearchTitle(ctx context.Context, t Title, season *int64) error {
	params := map[string]any{"title": t}
	if season != nil {
		params["season"] = *season
	}
	id := a.begin(ctx, "search_title", t.MediaType, firstNonZero(t.TmdbID, t.TvdbID), params)
	e, err := a.ResolveLibrary(ctx, t)
	if err != nil {
		return a.finish(ctx, id, err)
	}
	switch {
	case e.Kind == "radarr":
		if season != nil {
			return a.finish(ctx, id, errors.New("season applies to series only"))
		}
		err = e.Arr.Command(ctx, "MoviesSearch", map[string]any{"movieIds": []int64{e.ArrID}})
	case season != nil:
		err = e.Arr.Command(ctx, "SeasonSearch", map[string]any{"seriesId": e.ArrID, "seasonNumber": *season})
	default:
		err = e.Arr.Command(ctx, "SeriesSearch", map[string]any{"seriesId": e.ArrID})
	}
	if err == nil && a.Wake != nil {
		a.Wake()
	}
	return a.finish(ctx, id, err)
}

func firstNonZero(ids ...int64) int64 {
	for _, id := range ids {
		if id != 0 {
			return id
		}
	}
	return 0
}
