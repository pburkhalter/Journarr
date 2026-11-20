package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/api"
	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

const gib = 1 << 30

func gibs(n int64) float64 { return float64(n*10/gib) / 10 }

type noArgs struct{}

// TitleRef names a movie or series. TMDB ids come from search_titles.
type TitleRef struct {
	MediaType string `json:"media_type,omitempty" jsonschema:"movie or tv"`
	TmdbID    int64  `json:"tmdb_id,omitempty" jsonschema:"TMDB id (from search_titles); for series either this or tvdb_id"`
	TvdbID    int64  `json:"tvdb_id,omitempty" jsonschema:"TVDB id of a series (Sonarr's key); optional"`
}

func (t TitleRef) title() actions.Title {
	return actions.Title{MediaType: t.MediaType, TmdbID: t.TmdbID, TvdbID: t.TvdbID}
}

func (s *Server) registerReadTools() {
	addTool(s, &mcp.Tool{Name: "get_overview", Annotations: readOnly("Stack overview"),
		Description: "Health of every service in the stack (with the reason when one is degraded or down), " +
			"request counts per status, media items per stage, stuck items and free disk space. Start here."},
		false, s.getOverview)

	addTool(s, &mcp.Tool{Name: "list_requests", Annotations: readOnly("List requests"),
		Description: "Requests Journarr tracks, newest activity first. status: all (default), active, partial, " +
			"waiting (not released yet), done (completed/canceled/failed), completed, canceled, failed. " +
			"Each row has per-stage item counts, stuck count and the last error."},
		false, s.listRequests)

	addTool(s, &mcp.Tool{Name: "get_request", Annotations: readOnly("Request detail"),
		Description: "One request with every item (movie or episode) and its current stage, stuck flag and " +
			"last error, plus its downloads. Set include_transitions for each item's stage history."},
		false, s.getRequest)

	addTool(s, &mcp.Tool{Name: "search_titles", Annotations: readOnly("Search titles"),
		Description: "Search movies and series on TMDB (through Seerr). Returns tmdb ids and each title's " +
			"status: not_requested, pending, processing, partially_available or available."},
		false, s.searchTitles)

	addTool(s, &mcp.Tool{Name: "get_title", Annotations: readOnly("Title detail"),
		Description: "Everything known about one movie or series: TMDB facts and Seerr status, the " +
			"Sonarr/Radarr entry (monitored, quality profile, seasons with file counts, size, path) and " +
			"the Journarr requests for it."},
		false, s.getTitle)

	addTool(s, &mcp.Tool{Name: "list_library", Annotations: readOnly("List library"),
		Description: "Movies (Radarr) or series (Sonarr) in the library, sorted by title. Filters: query " +
			"(title substring), monitored, missing (movie without file / series with missing episodes), " +
			"quality_profile_id."},
		false, s.listLibrary)

	addTool(s, &mcp.Tool{Name: "get_queue", Annotations: readOnly("Download queue"),
		Description: "What Sonarr and Radarr are downloading right now, with progress, time left and errors."},
		false, s.getQueue)

	addTool(s, &mcp.Tool{Name: "get_calendar", Annotations: readOnly("Release calendar"),
		Description: "Episodes airing and movies releasing (cinema, digital, physical) in a date window " +
			"around today, with whether the file is already there."},
		false, s.getCalendar)

	addTool(s, &mcp.Tool{Name: "list_options", Annotations: readOnly("Profiles and folders"),
		Description: "Quality profiles and root folders of Sonarr and Radarr — the ids request_title and " +
			"update_title accept."},
		false, s.listOptions)
}

// ---- get_overview -------------------------------------------------------

type serviceOut struct {
	Service   string `json:"service"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Version   string `json:"version,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	CheckedAt string `json:"checked_at"`
	Update    string `json:"update_available,omitempty"`
}

func (s *Server) getOverview(ctx context.Context, _ noArgs) (any, error) {
	list, err := api.Services(ctx, s.d.Store, s.d.Updates)
	if err != nil {
		return nil, err
	}
	services := make([]serviceOut, 0, len(list))
	for _, h := range list {
		o := serviceOut{Service: h.Service, Status: h.Status, Version: h.Version, LatencyMS: h.LatencyMS,
			CheckedAt: h.CheckedAt.UTC().Format(time.RFC3339), Reason: healthReason(h.Detail)}
		if h.Update != nil && h.Update.UpdateAvailable {
			o.Update = h.Update.Latest
		}
		services = append(services, o)
	}
	stats, err := s.d.Store.FetchStats(ctx)
	if err != nil {
		return nil, err
	}
	stuck, _ := s.d.Store.CountStuck(ctx)
	out := map[string]any{
		"services":          services,
		"requests":          stats.Requests,
		"items_per_stage":   stats.MediaItems,
		"stuck_items":       stuck,
		"journarr_ui":       s.cfg.PublicURL,
		"removal_approvals": s.cfg.Remove,
	}
	if s.d.Approvals != nil && s.cfg.Remove == "approval" {
		if n, err := s.d.Approvals.CountPending(ctx); err == nil {
			out["pending_removal_requests"] = n
		}
	}
	if s.d.Storage != nil {
		type mount struct {
			Path   string  `json:"path"`
			FreeGB float64 `json:"free_gib"`
			Total  float64 `json:"total_gib,omitempty"`
		}
		var mounts []mount
		for _, m := range s.d.Storage.Mounts(ctx) {
			if !m.IsLibrary {
				continue
			}
			mounts = append(mounts, mount{Path: m.Path, FreeGB: gibs(m.FreeSpace), Total: gibs(m.TotalSpace)})
		}
		out["media_disks"] = mounts
	}
	return out, nil
}

// healthReason turns a service's health detail into one line.
func healthReason(detail string) string {
	if detail == "" {
		return ""
	}
	var d map[string]any
	if json.Unmarshal([]byte(detail), &d) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"error", "poller", "health_error"} {
		if v, ok := d[k].(string); ok && v != "" {
			parts = append(parts, v)
		}
	}
	if msgs, ok := d["health_messages"].([]any); ok {
		for _, m := range msgs {
			if s, ok := m.(string); ok {
				parts = append(parts, s)
			}
		}
	}
	if b, ok := d["restart_required"].(bool); ok && b {
		parts = append(parts, "Seerr asks for a restart")
	}
	return truncate(strings.Join(parts, "; "), 500)
}

// ---- requests -------------------------------------------------------------

type listRequestsIn struct {
	Status string `json:"status,omitempty" jsonschema:"all, active, partial, waiting, done, completed, canceled or failed"`
	Query  string `json:"query,omitempty" jsonschema:"title substring"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows, default 20, at most 100"`
	Offset int    `json:"offset,omitempty"`
}

type requestRow struct {
	ID          int64          `json:"request_id"`
	Title       string         `json:"title"`
	Year        *int64         `json:"year,omitempty"`
	MediaType   string         `json:"media_type"`
	TmdbID      *int64         `json:"tmdb_id,omitempty"`
	TvdbID      *int64         `json:"tvdb_id,omitempty"`
	Status      string         `json:"status"`
	RequestedBy string         `json:"requested_by,omitempty"`
	RequestedAt *time.Time     `json:"requested_at,omitempty"`
	Items       int            `json:"items"`
	Stages      map[string]int `json:"stages,omitempty"`
	Stuck       int            `json:"stuck,omitempty"`
	LastError   string         `json:"last_error,omitempty"`
	ReleaseAt   *time.Time     `json:"awaiting_release_at,omitempty"`
}

func rowOf(r store.RequestRollup) requestRow {
	return requestRow{ID: r.ID, Title: r.Title, Year: r.Year, MediaType: r.MediaType, TmdbID: r.TmdbID,
		TvdbID: r.TvdbID, Status: r.Status, RequestedBy: r.RequestedBy, RequestedAt: r.RequestedAt,
		Items: r.ItemCount, Stages: r.StageCounts, Stuck: r.StuckCount, LastError: r.LastError,
		ReleaseAt: r.AwaitingReleaseAt}
}

func page(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	return min(limit, 100), max(offset, 0)
}

func (s *Server) listRequests(ctx context.Context, in listRequestsIn) (any, error) {
	limit, offset := page(in.Limit, in.Offset)
	list, err := s.d.Store.ListRequests(ctx, in.Status, in.Query, limit+1, offset)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(list) > limit {
		list = list[:limit]
		out["next_offset"] = offset + limit
	}
	rows := make([]requestRow, 0, len(list))
	for _, r := range list {
		rows = append(rows, rowOf(r))
	}
	out["requests"] = rows
	return out, nil
}

type getRequestIn struct {
	RequestID          int64 `json:"request_id"`
	IncludeTransitions bool  `json:"include_transitions,omitempty" jsonschema:"add each item's stage history"`
}

func (s *Server) getRequest(ctx context.Context, in getRequestIn) (any, error) {
	d, err := api.LoadRequestDetail(ctx, s.d.Store, in.RequestID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("request %d not found", in.RequestID)
	}
	type itemOut struct {
		ID          int64                   `json:"media_item_id"`
		Kind        string                  `json:"kind"`
		Label       string                  `json:"label"`
		Stage       string                  `json:"stage"`
		StuckSince  *time.Time              `json:"stuck_since,omitempty"`
		LastError   string                  `json:"last_error,omitempty"`
		ReleaseAt   *time.Time              `json:"awaiting_release_at,omitempty"`
		Transitions []store.StageTransition `json:"transitions,omitempty"`
	}
	items := make([]itemOut, 0, len(d.Items))
	for _, it := range d.Items {
		o := itemOut{ID: it.ID, Kind: it.MediaType, Label: it.Title, Stage: it.CurrentStage,
			StuckSince: it.StuckSince, LastError: it.LastError, ReleaseAt: it.AwaitingReleaseAt}
		if it.SeasonNumber != nil && it.EpisodeNumber != nil {
			o.Label = fmt.Sprintf("S%02dE%02d %s", *it.SeasonNumber, *it.EpisodeNumber, it.Title)
		}
		if in.IncludeTransitions {
			o.Transitions = it.Transitions
		}
		items = append(items, o)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return map[string]any{"request": rowOf(*d.Request), "items": items, "downloads": d.Downloads}, nil
}

// ---- titles -----------------------------------------------------------------

type searchIn struct {
	Query string `json:"query" jsonschema:"title to search for"`
	Page  int    `json:"page,omitempty" jsonschema:"result page, default 1"`
}

func (s *Server) searchTitles(ctx context.Context, in searchIn) (any, error) {
	if s.d.Actions.Seerr == nil {
		return nil, errors.New("seerr is not configured")
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("query is required")
	}
	results, pages, err := s.d.Actions.Seerr.Search(ctx, in.Query, in.Page)
	if err != nil {
		return nil, err
	}
	type hit struct {
		MediaType string `json:"media_type"`
		TmdbID    int64  `json:"tmdb_id"`
		Title     string `json:"title"`
		Year      string `json:"year,omitempty"`
		Status    string `json:"status"`
		Overview  string `json:"overview,omitempty"`
	}
	hits := make([]hit, 0, len(results))
	for _, r := range results {
		status := "not_requested"
		if r.MediaInfo != nil {
			status = clients.SeerrMediaStatus(r.MediaInfo.Status)
		}
		hits = append(hits, hit{MediaType: r.MediaType, TmdbID: r.ID, Title: r.DisplayTitle(), Year: r.Year(),
			Status: status, Overview: truncate(r.Overview, 240)})
	}
	return map[string]any{"results": hits, "total_pages": pages}, nil
}

func (s *Server) getTitle(ctx context.Context, in TitleRef) (any, error) {
	t := in.title()
	if t.MediaType != "movie" && t.MediaType != "tv" {
		return nil, errors.New("media_type must be movie or tv")
	}
	out := map[string]any{"media_type": t.MediaType}
	if seerr := s.d.Actions.Seerr; seerr != nil && t.TmdbID != 0 {
		if d, err := seerr.Details(ctx, t.MediaType, t.TmdbID); err == nil {
			out["tmdb"] = tmdbView(d)
			if t.TvdbID == 0 {
				t.TvdbID = d.ExternalIDs.TvdbID
			}
		} else {
			out["tmdb_error"] = err.Error()
		}
	}
	entry, err := s.d.Actions.ResolveLibrary(ctx, t)
	switch {
	case errors.Is(err, actions.ErrNotInLibrary):
		out["library"] = "not in " + map[string]string{"movie": "Radarr", "tv": "Sonarr"}[t.MediaType]
	case err != nil:
		out["library_error"] = err.Error()
	default:
		out["library"] = s.libraryView(ctx, entry)
	}
	tmdb, tvdb := t.TmdbID, t.TvdbID
	reqs, err := s.d.Store.RequestsForTitle(ctx, t.MediaType, &tmdb, &tvdb)
	if err == nil {
		type req struct {
			ID     int64  `json:"request_id"`
			Status string `json:"status"`
			By     string `json:"requested_by,omitempty"`
		}
		var rs []req
		for _, r := range reqs {
			rs = append(rs, req{ID: r.ID, Status: r.Status, By: r.RequestedBy})
		}
		out["journarr_requests"] = rs
	}
	return out, nil
}

func tmdbView(d *clients.SeerrDetails) map[string]any {
	title, date := d.Title, d.ReleaseDate
	if title == "" {
		title, date = d.Name, d.FirstAirDate
	}
	v := map[string]any{"title": title, "date": date, "overview": truncate(d.Overview, 400), "tmdb_status": d.Status}
	var genres []string
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}
	if len(genres) > 0 {
		v["genres"] = genres
	}
	if d.Runtime > 0 {
		v["runtime_min"] = d.Runtime
	}
	if d.NumberOfSeasons > 0 {
		v["seasons"], v["episodes"] = d.NumberOfSeasons, d.NumberOfEpisodes
	}
	v["status"] = "not_requested"
	if d.MediaInfo != nil {
		v["status"] = clients.SeerrMediaStatus(d.MediaInfo.Status)
		if len(d.MediaInfo.Seasons) > 0 {
			seasons := map[string]string{}
			for _, s := range d.MediaInfo.Seasons {
				seasons[fmt.Sprint(s.SeasonNumber)] = clients.SeerrMediaStatus(s.Status)
			}
			v["season_status"] = seasons
		}
	}
	if d.ExternalIDs.TvdbID != 0 {
		v["tvdb_id"] = d.ExternalIDs.TvdbID
	}
	return v
}

// libraryView is the Sonarr/Radarr side of a title.
func (s *Server) libraryView(ctx context.Context, e *actions.LibraryEntry) map[string]any {
	profiles := s.profileNames(ctx, e.Arr)
	if m := e.Movie; m != nil {
		v := map[string]any{"arr": "radarr", "radarr_id": m.ID, "title": m.Title, "year": m.Year,
			"monitored": m.Monitored, "has_file": m.HasFile, "size_gib": gibs(m.SizeOnDisk),
			"quality_profile": profiles[m.QualityProfileID], "quality_profile_id": m.QualityProfileID,
			"minimum_availability": m.MinimumAvailability, "status": m.Status, "path": m.Path,
			"in_cinemas": m.InCinemas, "digital_release": m.DigitalRelease, "physical_release": m.PhysicalRelease}
		if m.MovieFile != nil {
			v["file_quality"] = m.MovieFile.Quality.Quality.Name
		}
		return v
	}
	sr := e.Series
	type season struct {
		Number    int64   `json:"season"`
		Monitored bool    `json:"monitored"`
		Files     int64   `json:"files"`
		Episodes  int64   `json:"episodes"`
		Total     int64   `json:"total_episodes"`
		SizeGiB   float64 `json:"size_gib"`
	}
	var seasons []season
	for _, x := range sr.Seasons {
		seasons = append(seasons, season{Number: x.SeasonNumber, Monitored: x.Monitored,
			Files: x.Statistics.EpisodeFileCount, Episodes: x.Statistics.EpisodeCount,
			Total: x.Statistics.TotalEpisodeCount, SizeGiB: gibs(x.Statistics.SizeOnDisk)})
	}
	return map[string]any{"arr": "sonarr", "sonarr_id": sr.ID, "tvdb_id": sr.TvdbID, "title": sr.Title,
		"year": sr.Year, "monitored": sr.Monitored, "status": sr.Status,
		"quality_profile": profiles[sr.QualityProfileID], "quality_profile_id": sr.QualityProfileID,
		"size_gib": gibs(sr.Statistics.SizeOnDisk), "path": sr.Path, "next_airing": sr.NextAiring,
		"seasons": seasons}
}

func (s *Server) profileNames(ctx context.Context, arr *clients.Arr) map[int64]string {
	names := map[int64]string{}
	if arr == nil {
		return names
	}
	if ps, err := arr.QualityProfiles(ctx); err == nil {
		for _, p := range ps {
			names[p.ID] = p.Name
		}
	}
	return names
}

// ---- library ----------------------------------------------------------------

type listLibraryIn struct {
	MediaType        string `json:"media_type" jsonschema:"movie or tv"`
	Query            string `json:"query,omitempty" jsonschema:"title substring, case-insensitive"`
	Monitored        *bool  `json:"monitored,omitempty"`
	Missing          bool   `json:"missing,omitempty" jsonschema:"only movies without file / series with missing aired episodes"`
	QualityProfileID int64  `json:"quality_profile_id,omitempty"`
	Limit            int    `json:"limit,omitempty" jsonschema:"max rows, default 20, at most 100"`
	Offset           int    `json:"offset,omitempty"`
}

func (s *Server) listLibrary(ctx context.Context, in listLibraryIn) (any, error) {
	limit, offset := page(in.Limit, in.Offset)
	q := strings.ToLower(strings.TrimSpace(in.Query))
	keep := func(title string, monitored bool, profile int64, missing bool) bool {
		return (q == "" || strings.Contains(strings.ToLower(title), q)) &&
			(in.Monitored == nil || *in.Monitored == monitored) &&
			(in.QualityProfileID == 0 || in.QualityProfileID == profile) &&
			(!in.Missing || missing)
	}
	var rows []map[string]any
	switch in.MediaType {
	case "movie":
		arr := s.d.Actions.Radarr
		if arr == nil {
			return nil, errors.New("radarr is not configured")
		}
		movies, err := arr.Movies(ctx)
		if err != nil {
			return nil, err
		}
		profiles := s.profileNames(ctx, arr)
		sort.Slice(movies, func(i, j int) bool { return movies[i].Title < movies[j].Title })
		for _, m := range movies {
			if keep(m.Title, m.Monitored, m.QualityProfileID, !m.HasFile) {
				rows = append(rows, map[string]any{"tmdb_id": m.TmdbID, "title": m.Title, "year": m.Year,
					"monitored": m.Monitored, "has_file": m.HasFile, "size_gib": gibs(m.SizeOnDisk),
					"quality_profile": profiles[m.QualityProfileID], "status": m.Status})
			}
		}
	case "tv":
		arr := s.d.Actions.Sonarr
		if arr == nil {
			return nil, errors.New("sonarr is not configured")
		}
		series, err := arr.AllSeries(ctx)
		if err != nil {
			return nil, err
		}
		profiles := s.profileNames(ctx, arr)
		sort.Slice(series, func(i, j int) bool { return series[i].Title < series[j].Title })
		for _, sr := range series {
			st := sr.Statistics
			if keep(sr.Title, sr.Monitored, sr.QualityProfileID, st.EpisodeFileCount < st.EpisodeCount) {
				rows = append(rows, map[string]any{"tvdb_id": sr.TvdbID, "tmdb_id": sr.TmdbID, "title": sr.Title,
					"year": sr.Year, "monitored": sr.Monitored, "status": sr.Status,
					"episodes": fmt.Sprintf("%d/%d", st.EpisodeFileCount, st.EpisodeCount),
					"size_gib": gibs(st.SizeOnDisk), "quality_profile": profiles[sr.QualityProfileID]})
			}
		}
	default:
		return nil, errors.New("media_type must be movie or tv")
	}
	out := map[string]any{"total": len(rows)}
	end := min(offset+limit, len(rows))
	if offset < len(rows) {
		out["items"] = rows[offset:end]
	} else {
		out["items"] = []any{}
	}
	if end < len(rows) {
		out["next_offset"] = end
	}
	return out, nil
}

// ---- queue, calendar, options -------------------------------------------------

func (s *Server) getQueue(ctx context.Context, _ noArgs) (any, error) {
	type row struct {
		Arr      string  `json:"arr"`
		Title    string  `json:"title"`
		Status   string  `json:"status"`
		State    string  `json:"state,omitempty"`
		Progress float64 `json:"progress_pct"`
		SizeGiB  float64 `json:"size_gib"`
		TimeLeft string  `json:"time_left,omitempty"`
		Protocol string  `json:"protocol,omitempty"`
		Error    string  `json:"error,omitempty"`
	}
	var rows []row
	var errs []string
	for _, arr := range []*clients.Arr{s.d.Actions.Sonarr, s.d.Actions.Radarr} {
		if arr == nil {
			continue
		}
		queue, err := arr.Queue(ctx)
		if err != nil {
			errs = append(errs, arr.Type()+": "+err.Error())
			continue
		}
		seen := map[string]bool{}
		for _, q := range queue {
			key := q.DownloadID
			if key == "" {
				key = fmt.Sprint(q.ID)
			}
			if seen[key] { // a season pack lists once per episode
				continue
			}
			seen[key] = true
			r := row{Arr: arr.Type(), Title: q.Title, Status: q.Status, State: q.TrackedDownloadState,
				SizeGiB: gibs(int64(q.Size)), TimeLeft: q.TimeLeft, Protocol: q.Protocol, Error: q.ErrorMessage}
			if q.Size > 0 {
				r.Progress = float64(int((q.Size-q.SizeLeft)/q.Size*1000)) / 10
			}
			rows = append(rows, r)
		}
	}
	out := map[string]any{"downloads": rows}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return out, nil
}

type calendarIn struct {
	DaysAhead int `json:"days_ahead,omitempty" jsonschema:"days after today, default 7, at most 60"`
	DaysBack  int `json:"days_back,omitempty" jsonschema:"days before today, default 0, at most 30"`
}

func (s *Server) getCalendar(ctx context.Context, in calendarIn) (any, error) {
	ahead := in.DaysAhead
	if ahead <= 0 {
		ahead = 7
	}
	ahead, back := min(ahead, 60), min(max(in.DaysBack, 0), 30)
	today := time.Now().Truncate(24 * time.Hour)
	start, end := today.AddDate(0, 0, -back), today.AddDate(0, 0, ahead+1)
	out := map[string]any{"from": start.Format("2006-01-02"), "to": end.Format("2006-01-02")}
	var errs []string
	if arr := s.d.Actions.Sonarr; arr != nil {
		eps, err := arr.EpisodeCalendar(ctx, start, end)
		if err != nil {
			errs = append(errs, "sonarr: "+err.Error())
		}
		type ep struct {
			Air       string `json:"air_date_utc"`
			Series    string `json:"series"`
			Episode   string `json:"episode"`
			HasFile   bool   `json:"has_file"`
			Monitored bool   `json:"monitored"`
		}
		var list []ep
		for _, e := range eps {
			list = append(list, ep{Air: e.AirDateUTC, Series: e.Series.Title,
				Episode: fmt.Sprintf("S%02dE%02d %s", e.SeasonNumber, e.EpisodeNumber, e.Title),
				HasFile: e.HasFile, Monitored: e.Monitored})
		}
		out["episodes"] = list
	}
	if arr := s.d.Actions.Radarr; arr != nil {
		movies, err := arr.MovieCalendar(ctx, start, end)
		if err != nil {
			errs = append(errs, "radarr: "+err.Error())
		}
		type mv struct {
			Title     string `json:"title"`
			TmdbID    int64  `json:"tmdb_id"`
			InCinemas string `json:"in_cinemas,omitempty"`
			Digital   string `json:"digital_release,omitempty"`
			Physical  string `json:"physical_release,omitempty"`
			HasFile   bool   `json:"has_file"`
			Monitored bool   `json:"monitored"`
		}
		var list []mv
		for _, m := range movies {
			list = append(list, mv{Title: m.Title, TmdbID: m.TmdbID, InCinemas: m.InCinemas,
				Digital: m.DigitalRelease, Physical: m.PhysicalRelease, HasFile: m.HasFile, Monitored: m.Monitored})
		}
		out["movies"] = list
	}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return out, nil
}

func (s *Server) listOptions(ctx context.Context, _ noArgs) (any, error) {
	out := map[string]any{}
	for name, arr := range map[string]*clients.Arr{"sonarr": s.d.Actions.Sonarr, "radarr": s.d.Actions.Radarr} {
		if arr == nil {
			continue
		}
		v := map[string]any{}
		if ps, err := arr.QualityProfiles(ctx); err == nil {
			v["quality_profiles"] = ps
		} else {
			v["quality_profiles_error"] = err.Error()
		}
		if rf, err := arr.RootFolders(ctx); err == nil {
			type folder struct {
				Path    string  `json:"path"`
				FreeGiB float64 `json:"free_gib"`
			}
			var fs []folder
			for _, f := range rf {
				fs = append(fs, folder{Path: f.Path, FreeGiB: gibs(f.FreeSpace)})
			}
			v["root_folders"] = fs
		} else {
			v["root_folders_error"] = err.Error()
		}
		if name == "radarr" {
			v["minimum_availability"] = []string{"announced", "inCinemas", "released"}
		}
		out[name] = v
	}
	return out, nil
}
