package actions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// RecheckSearchCooldown keeps a repeated re-check (the button pressed twice,
// an MCP client in a loop) from searching the indexers again right away. The
// rest of a re-check always runs.
const RecheckSearchCooldown = 15 * time.Minute

// seasonSearchFrom is the number of missing episodes in one season from which
// a re-check asks Sonarr for the whole season (one query that also finds a
// season pack) instead of searching the episodes one by one.
const seasonSearchFrom = 4

// maxRecheckErrors caps the error list: with Sonarr down every episode fails
// the same way.
const maxRecheckErrors = 10

// Item outcomes of a re-check. An item can have several: "no_file" and/or
// "reset", then what its attempt really is.
const (
	outError       = "error"       // Sonarr/Radarr or the store could not answer
	outNoFile      = "no_file"     // shown in the library without an import or a file: taken back
	outNotInArr    = "not_in_arr"  // Sonarr/Radarr do not know the title or episode
	outReconciled  = "reconciled"  // the file was already there: moved to imported
	outReset       = "reset"       // every download of the attempt failed: new attempt
	outMissing     = "missing"     // out, monitored, no file: searched
	outUnmonitored = "unmonitored" // Sonarr/Radarr do not monitor it, nothing searched
	outDownloading = "downloading" // a download is in flight
	outWaiting     = "waiting"     // not released or not aired yet
	outDone        = "done"        // already imported
)

// outcomeRank orders the outcomes by how much they tell about a request; the
// first one present becomes the request's outcome.
var outcomeRank = []string{outError, outNoFile, outNotInArr, outReconciled, outReset, outMissing,
	outUnmonitored, outDownloading, outWaiting, outDone}

// RecheckReport is what a re-check of the open requests found and did. The
// counts are items (movies or episodes); Open counts the requests checked.
type RecheckReport struct {
	Open    int `json:"open"`
	Missing int `json:"missing"`
	// NoFile: shown as imported or later without an import in the current
	// attempt (Jellyfin, Seerr), and Sonarr/Radarr have no file. Taken back to
	// the stage the attempt really reached.
	NoFile      int `json:"no_file"`
	Reset       int `json:"reset"`
	Reconciled  int `json:"reconciled"`
	Waiting     int `json:"waiting"`
	Downloading int `json:"downloading"`
	Unmonitored int `json:"unmonitored"`
	NotInArr    int `json:"not_in_arr"`
	// SearchSkipped: an earlier re-check searched at SearchedAt, less than
	// RecheckSearchCooldown ago, so this one did not search again.
	SearchSkipped bool             `json:"search_skipped,omitempty"`
	SearchedAt    *time.Time       `json:"searched_at,omitempty"`
	Errors        []string         `json:"errors,omitempty"`
	Requests      []RecheckRequest `json:"requests"`
}

// RecheckRequest is the outcome for one open request.
type RecheckRequest struct {
	ID        int64          `json:"id"`
	Title     string         `json:"title"`
	MediaType string         `json:"media_type"`
	Outcome   string         `json:"outcome"`
	Counts    map[string]int `json:"counts"` // item outcome → items
}

// Summary is the one-line audit detail.
func (r *RecheckReport) Summary() string {
	parts := []string{fmt.Sprintf("%d open", r.Open)}
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	missing := "missing (searched)"
	if r.SearchSkipped {
		missing = "missing (searched shortly before, not again)"
	}
	add(r.NoFile, "shown as available without a file (taken back)")
	add(r.Missing, missing)
	add(r.Reconciled, "already on disk")
	add(r.Reset, "failed attempts reset")
	add(r.Downloading, "downloading")
	add(r.Waiting, "not out yet")
	add(r.Unmonitored, "unmonitored")
	add(r.NotInArr, "unknown to Sonarr/Radarr")
	add(len(r.Errors), "errors")
	return strings.Join(parts, " · ")
}

// RecheckOpen squares every unfinished item of the open requests with
// Sonarr/Radarr: a file already on disk moves the item to imported, an
// attempt whose every download failed starts a new one, and whatever is out
// (released or aired), monitored and still missing gets one search. Items
// that are not out yet, downloading or unmonitored are only counted.
//
// It also checks the items shown in the library without an import in their
// current attempt, in any request that is not canceled or failed (such a
// request may show as completed): without a file in Sonarr/Radarr they go
// back to the stage their attempt really reached and are then checked like
// any unfinished item.
func (a *Actions) RecheckOpen(ctx context.Context) (*RecheckReport, error) {
	id := a.begin(ctx, "recheck_open", "", 0, nil)
	rep, err := a.recheckOpen(ctx)
	detail := ""
	if rep != nil {
		detail = rep.Summary()
	}
	return rep, a.finishDetail(ctx, id, err, detail)
}

type seasonKey struct{ series, season int64 }

// arrState is what Sonarr/Radarr say about one item.
type arrState struct {
	arr                                string
	known, hasFile, monitored, waiting bool
	queue                              func() // adds the item to the searches
}

type recheck struct {
	a           *Actions
	now         time.Time
	ord         map[string]int
	episodes    map[int64][]clients.Episode
	episodeErrs map[int64]error
	movieIDs    []int64
	seasons     map[seasonKey][]int64
	touched     map[int64]bool
	errSeen     map[string]bool
	rep         *RecheckReport
}

func (a *Actions) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Actions) recheckOpen(ctx context.Context) (*RecheckReport, error) {
	reqs, err := a.Store.OpenRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open requests: %w", err)
	}
	unconfirmed, err := a.Store.UnconfirmedRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unconfirmed requests: %w", err)
	}
	reqs = mergeRequests(reqs, unconfirmed)
	ord, err := a.Store.LoadStageOrdinals(ctx)
	if err != nil {
		return nil, fmt.Errorf("load stages: %w", err)
	}
	rc := &recheck{
		a: a, now: a.now(), ord: ord,
		episodes: map[int64][]clients.Episode{}, episodeErrs: map[int64]error{},
		seasons: map[seasonKey][]int64{}, touched: map[int64]bool{}, errSeen: map[string]bool{},
		rep: &RecheckReport{Open: len(reqs), Requests: []RecheckRequest{}},
	}
	for _, r := range reqs {
		rc.request(ctx, r)
	}
	rc.search(ctx)
	for reqID := range rc.touched {
		status, err := a.Store.RecomputeRequestStatus(ctx, reqID)
		if err != nil {
			a.Log.Warn("recheck: recompute status", "request", reqID, "err", err)
			continue
		}
		if a.Publish != nil {
			a.Publish("request.updated", map[string]any{"id": reqID, "status": status})
		}
	}
	return rc.rep, nil
}

// mergeRequests joins two id-sorted request lists without duplicates.
func mergeRequests(a, b []store.Request) []store.Request {
	seen := make(map[int64]bool, len(a))
	out := append([]store.Request(nil), a...)
	for _, r := range a {
		seen[r.ID] = true
	}
	for _, r := range b {
		if !seen[r.ID] {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(x, y store.Request) int { return cmp.Compare(x.ID, y.ID) })
	return out
}

func (rc *recheck) request(ctx context.Context, r store.Request) {
	out := RecheckRequest{ID: r.ID, Title: r.Title, MediaType: r.MediaType, Counts: map[string]int{}}
	items, err := rc.a.Store.ListItemsForRequest(ctx, r.ID)
	switch {
	case err != nil:
		rc.count(&out, rc.errorf(r.Title, err))
	case len(items) == 0:
		rc.count(&out, rc.titleOnly(ctx, r))
	default:
		for i := range items {
			for _, o := range rc.item(ctx, &items[i]) {
				rc.count(&out, o)
			}
		}
	}
	for _, o := range outcomeRank {
		if out.Counts[o] > 0 {
			out.Outcome = o
			break
		}
	}
	rc.rep.Requests = append(rc.rep.Requests, out)
}

func (rc *recheck) count(r *RecheckRequest, outcome string) {
	r.Counts[outcome]++
	switch outcome {
	case outNoFile:
		rc.rep.NoFile++
	case outMissing:
		rc.rep.Missing++
	case outReset:
		rc.rep.Reset++
	case outReconciled:
		rc.rep.Reconciled++
	case outWaiting:
		rc.rep.Waiting++
	case outDownloading:
		rc.rep.Downloading++
	case outUnmonitored:
		rc.rep.Unmonitored++
	case outNotInArr:
		rc.rep.NotInArr++
	}
}

func (rc *recheck) errorf(what string, err error) string {
	msg := fmt.Sprintf("%s: %v", what, err)
	if !rc.errSeen[msg] && len(rc.rep.Errors) < maxRecheckErrors {
		rc.errSeen[msg] = true
		rc.rep.Errors = append(rc.rep.Errors, msg)
	}
	return outError
}

// titleOnly classifies a request without items. Journarr creates them once
// Sonarr/Radarr have the title, so the usual reason is that Seerr could not
// add it there; a series Sonarr knows without episodes is waiting for them.
func (rc *recheck) titleOnly(ctx context.Context, r store.Request) string {
	switch {
	case r.MediaType == "movie" && r.TmdbID != nil && rc.a.Radarr != nil:
		mv, err := rc.a.Radarr.LookupMovieByTmdb(ctx, *r.TmdbID)
		if err != nil {
			return rc.errorf(r.Title, err)
		}
		if mv == nil {
			return outNotInArr
		}
	case r.MediaType == "tv" && r.TvdbID != nil && rc.a.Sonarr != nil:
		s, err := rc.a.Sonarr.SeriesByTvdbID(ctx, *r.TvdbID)
		if err != nil {
			return rc.errorf(r.Title, err)
		}
		if s == nil {
			return outNotInArr
		}
	default:
		return outNotInArr
	}
	return outWaiting
}

func (rc *recheck) item(ctx context.Context, it *store.MediaItem) []string {
	past := rc.ord[it.CurrentStage] >= rc.ord["imported"]
	if past {
		// Imported in this attempt: the file came through Sonarr/Radarr.
		imported, err := rc.a.Store.HasTransition(ctx, it.ID, it.CurrentCycle, "imported")
		if err != nil {
			return []string{rc.errorf(itemLabel(it), err)}
		}
		if imported {
			return []string{outDone}
		}
	}

	var st arrState
	var err error
	if it.MediaType == "movie" {
		st, err = rc.movie(ctx, it)
	} else {
		st, err = rc.episode(ctx, it)
	}
	if err != nil {
		return []string{rc.errorf(itemLabel(it), err)}
	}

	var out []string
	if past {
		// Only Jellyfin, Seerr or the presence check said it is there; the
		// file in Sonarr/Radarr decides. Without one the claim is taken back.
		if !st.known || st.hasFile {
			return []string{outDone}
		}
		stage, removed, err := rc.a.Store.RewindUnconfirmed(ctx, it.ID)
		if err != nil {
			return []string{rc.errorf(itemLabel(it), err)}
		}
		rc.a.Log.Info("recheck: shown in the library without a file, taken back",
			"item", it.ID, "label", itemLabel(it), "from", it.CurrentStage, "to", stage,
			"cycle", it.CurrentCycle, "transitions_removed", removed)
		it.CurrentStage = stage
		rc.touch(it)
		out = append(out, outNoFile)
	}

	dls, err := rc.a.Store.DownloadsForItemCycle(ctx, it.ID, it.CurrentCycle)
	if err != nil {
		return append(out, rc.errorf(itemLabel(it), err))
	}
	inFlight, allFailed := false, len(dls) > 0
	for _, d := range dls {
		if !isTerminal(d.State) {
			inFlight = true
		}
		if d.State != "failed" {
			allFailed = false
		}
	}

	switch {
	case !st.known:
		return append(out, outNotInArr)
	case inFlight:
		return append(out, outDownloading)
	case st.hasFile:
		if _, err := rc.a.Store.ApplyStage(ctx, it.ID, it.CurrentCycle, "imported", 0,
			"re-check: file already in "+st.arr); err != nil {
			return append(out, rc.errorf(itemLabel(it), err))
		}
		rc.touch(it)
		return append(out, outReconciled)
	}

	if allFailed {
		if _, err := rc.a.Store.BumpItemCycle(ctx, it.ID); err != nil {
			return []string{rc.errorf(itemLabel(it), err)}
		}
		rc.touch(it)
		out = append(out, outReset)
	}
	switch {
	case !st.monitored:
		out = append(out, outUnmonitored)
	case st.waiting:
		out = append(out, outWaiting)
	default:
		st.queue()
		out = append(out, outMissing)
	}
	return out
}

func (rc *recheck) touch(it *store.MediaItem) {
	if it.RequestID != nil {
		rc.touched[*it.RequestID] = true
	}
}

func (rc *recheck) movie(ctx context.Context, it *store.MediaItem) (arrState, error) {
	st := arrState{arr: "Radarr"}
	if rc.a.Radarr == nil {
		return st, errors.New("radarr not configured")
	}
	if it.TmdbID == nil {
		return st, nil
	}
	mv, err := rc.a.Radarr.LookupMovieByTmdb(ctx, *it.TmdbID)
	if err != nil || mv == nil {
		return st, err
	}
	if it.RadarrMovieID == nil {
		if err := rc.a.Store.SetItemRadarrMovieID(ctx, it.ID, mv.ID); err != nil {
			rc.a.Log.Warn("recheck: persist radarr movie id", "item", it.ID, "err", err)
		}
	}
	_, waiting := mv.Waiting(rc.now)
	st.known, st.hasFile, st.monitored, st.waiting = true, mv.HasFile, mv.Monitored, waiting
	st.queue = func() { rc.movieIDs = append(rc.movieIDs, mv.ID) }
	return st, nil
}

func (rc *recheck) episode(ctx context.Context, it *store.MediaItem) (arrState, error) {
	st := arrState{arr: "Sonarr"}
	if rc.a.Sonarr == nil {
		return st, errors.New("sonarr not configured")
	}
	var seriesID int64
	if it.SonarrSeriesID != nil {
		seriesID = *it.SonarrSeriesID
	}
	if seriesID == 0 && it.TvdbID != nil {
		s, err := rc.a.Sonarr.SeriesByTvdbID(ctx, *it.TvdbID)
		if err != nil || s == nil {
			return st, err
		}
		seriesID = s.ID
	}
	if seriesID == 0 {
		return st, nil
	}
	eps, err := rc.seriesEpisodes(ctx, seriesID)
	if err != nil {
		return st, err
	}
	ep, ok := findEpisode(eps, it)
	if !ok {
		return st, nil
	}
	aired := false
	if t, err := time.Parse(time.RFC3339, ep.AirDateUTC); err == nil && !t.After(rc.now) {
		aired = true
	}
	st.known, st.hasFile, st.monitored, st.waiting = true, ep.HasFile, ep.Monitored, !aired
	st.queue = func() {
		k := seasonKey{seriesID, ep.SeasonNumber}
		rc.seasons[k] = append(rc.seasons[k], ep.ID)
	}
	return st, nil
}

// seriesEpisodes fetches a series' episodes once per re-check (an error too).
func (rc *recheck) seriesEpisodes(ctx context.Context, seriesID int64) ([]clients.Episode, error) {
	if err, ok := rc.episodeErrs[seriesID]; ok {
		return nil, err
	}
	if eps, ok := rc.episodes[seriesID]; ok {
		return eps, nil
	}
	eps, err := rc.a.Sonarr.EpisodesBySeries(ctx, seriesID)
	if err != nil {
		rc.episodeErrs[seriesID] = err
		return nil, err
	}
	rc.episodes[seriesID] = eps
	return eps, nil
}

func findEpisode(eps []clients.Episode, it *store.MediaItem) (clients.Episode, bool) {
	for _, ep := range eps {
		if it.SonarrEpisodeID != nil {
			if ep.ID == *it.SonarrEpisodeID {
				return ep, true
			}
			continue
		}
		if it.SeasonNumber != nil && it.EpisodeNumber != nil &&
			ep.SeasonNumber == *it.SeasonNumber && ep.EpisodeNumber == *it.EpisodeNumber {
			return ep, true
		}
	}
	return clients.Episode{}, false
}

func itemLabel(it *store.MediaItem) string {
	if it.MediaType == "episode" && it.SeasonNumber != nil && it.EpisodeNumber != nil {
		return fmt.Sprintf("%s S%02dE%02d", it.Title, *it.SeasonNumber, *it.EpisodeNumber)
	}
	return it.Title
}

// search starts the collected searches: one Radarr command for every movie,
// per season one Sonarr command — the season at once when several of its
// episodes are missing, else the episodes themselves.
func (rc *recheck) search(ctx context.Context) {
	if len(rc.movieIDs) == 0 && len(rc.seasons) == 0 {
		return
	}
	if last, ok := rc.a.claimRecheckSearch(rc.now); !ok {
		rc.rep.SearchSkipped, rc.rep.SearchedAt = true, &last
		return
	}
	now := rc.now
	rc.rep.SearchedAt = &now
	if len(rc.movieIDs) > 0 {
		slices.Sort(rc.movieIDs)
		if err := rc.a.Radarr.Command(ctx, "MoviesSearch", map[string]any{"movieIds": rc.movieIDs}); err != nil {
			rc.errorf("Radarr search", err)
		}
	}
	keys := make([]seasonKey, 0, len(rc.seasons))
	for k := range rc.seasons {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y seasonKey) int {
		if c := cmp.Compare(x.series, y.series); c != 0 {
			return c
		}
		return cmp.Compare(x.season, y.season)
	})
	for _, k := range keys {
		eps := rc.seasons[k]
		var err error
		if len(eps) >= seasonSearchFrom {
			err = rc.a.Sonarr.Command(ctx, "SeasonSearch", map[string]any{"seriesId": k.series, "seasonNumber": k.season})
		} else {
			err = rc.a.Sonarr.Command(ctx, "EpisodeSearch", map[string]any{"episodeIds": eps})
		}
		if err != nil {
			rc.errorf(fmt.Sprintf("Sonarr search (series %d, season %d)", k.series, k.season), err)
		}
	}
	if rc.a.Wake != nil {
		rc.a.Wake()
	}
}

// claimRecheckSearch reports whether a re-check may search now and, if so,
// starts the cooldown. Otherwise it returns when the last search ran.
func (a *Actions) claimRecheckSearch(now time.Time) (time.Time, bool) {
	a.recheckMu.Lock()
	defer a.recheckMu.Unlock()
	if !a.lastRecheckSearch.IsZero() && now.Sub(a.lastRecheckSearch) < RecheckSearchCooldown {
		return a.lastRecheckSearch, false
	}
	a.lastRecheckSearch = now
	return now, true
}
