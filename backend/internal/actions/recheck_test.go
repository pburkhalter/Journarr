package actions

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/store"
)

// fakeArr serves the few Sonarr/Radarr endpoints a re-check reads and records
// every command it receives.
type fakeArr struct {
	mu       sync.Mutex
	movies   map[string]map[string]any // tmdbId → movie
	series   map[string]map[string]any // tvdbId → series
	episodes map[string][]map[string]any
	commands []map[string]any
}

func (f *fakeArr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	list := func(m map[string]any) []map[string]any {
		if m == nil {
			return []map[string]any{}
		}
		return []map[string]any{m}
	}
	switch {
	case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.commands = append(f.commands, body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == "/api/v3/movie":
		_ = json.NewEncoder(w).Encode(list(f.movies[q.Get("tmdbId")]))
	case r.URL.Path == "/api/v3/series":
		_ = json.NewEncoder(w).Encode(list(f.series[q.Get("tvdbId")]))
	case r.URL.Path == "/api/v3/episode":
		eps := f.episodes[q.Get("seriesId")]
		if eps == nil {
			eps = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(eps)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeArr) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.commands...)
}

func arrClient(t *testing.T, kind string, h http.Handler) *clients.Arr {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &clients.Arr{Name: kind, Kind: kind, BaseURL: srv.URL, APIBase: "/api/v3", APIKey: "k",
		HTTP: &http.Client{Timeout: 5 * time.Second}}
}

func i64(v int64) *int64 { return &v }

// One re-check over every case it distinguishes: a file already on disk, a
// failed attempt, a film not out yet, a title Radarr does not know, a running
// download, an unmonitored film, a series with a gap of a whole season and a
// single episode, and a series Sonarr never got. A second run right after
// does not search again.
func TestRecheckOpen(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	past, future := "2026-01-01T00:00:00Z", "2027-06-01T00:00:00Z"

	radarr := &fakeArr{movies: map[string]map[string]any{
		"1": {"id": 11, "tmdbId": 1, "hasFile": true, "monitored": true, "isAvailable": true, "digitalRelease": past},
		"2": {"id": 12, "tmdbId": 2, "hasFile": false, "monitored": true, "isAvailable": true, "digitalRelease": past},
		"3": {"id": 13, "tmdbId": 3, "hasFile": false, "monitored": true, "isAvailable": true, "digitalRelease": future},
		"5": {"id": 15, "tmdbId": 5, "hasFile": false, "monitored": true, "isAvailable": true, "digitalRelease": past},
		"6": {"id": 16, "tmdbId": 6, "hasFile": false, "monitored": false, "isAvailable": true, "digitalRelease": past},
	}}
	sonarr := &fakeArr{
		series: map[string]map[string]any{"100": {"id": 7, "tvdbId": 100}},
		episodes: map[string][]map[string]any{"7": {
			{"id": 701, "seriesId": 7, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": past, "monitored": true},
			{"id": 702, "seriesId": 7, "seasonNumber": 1, "episodeNumber": 2, "airDateUtc": past, "monitored": true},
			{"id": 703, "seriesId": 7, "seasonNumber": 1, "episodeNumber": 3, "airDateUtc": past, "monitored": true},
			{"id": 704, "seriesId": 7, "seasonNumber": 1, "episodeNumber": 4, "airDateUtc": past, "monitored": true},
			{"id": 711, "seriesId": 7, "seasonNumber": 2, "episodeNumber": 1, "airDateUtc": past, "monitored": true},
			{"id": 712, "seriesId": 7, "seasonNumber": 2, "episodeNumber": 2, "airDateUtc": future, "monitored": true},
		}},
	}
	a := &Actions{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Radarr: arrClient(t, "radarr", radarr), Sonarr: arrClient(t, "sonarr", sonarr),
		Now: func() time.Time { return now }}

	seerr := int64(0)
	request := func(mediaType, title string, tmdb, tvdb *int64) int64 {
		seerr++
		id, err := st.UpsertRequest(ctx, store.Request{SeerrRequestID: i64(seerr), MediaType: mediaType,
			TmdbID: tmdb, TvdbID: tvdb, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	movie := func(title string, tmdb int64) (reqID, itemID int64) {
		reqID = request("movie", title, i64(tmdb), nil)
		itemID, err := st.EnsureMediaItem(ctx, store.MediaItem{RequestID: &reqID, MediaType: "movie",
			TmdbID: i64(tmdb), Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return reqID, itemID
	}
	download := func(itemID int64, clientID, state string) {
		dl, err := st.UpsertDownload(ctx, store.Download{ClientDownloadID: clientID, Arr: "radarr"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.LinkDownloadItem(ctx, dl, itemID, 1); err != nil {
			t.Fatal(err)
		}
		if state != "grabbed" {
			if err := st.SetDownloadState(ctx, dl, state, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	onDiskReq, onDisk := movie("On Disk", 1)
	_, failed := movie("Failed", 2)
	if _, err := st.ApplyStage(ctx, failed, 1, "grabbed", 0, ""); err != nil {
		t.Fatal(err)
	}
	download(failed, "dl-failed", "failed")
	movie("Not Out", 3)
	movie("Unknown", 4)
	_, running := movie("Running", 5)
	download(running, "dl-running", "grabbed")
	movie("Unmonitored", 6)

	tvReq := request("tv", "Series", nil, i64(100))
	for _, ep := range []struct{ id, season, episode int64 }{
		{701, 1, 1}, {702, 1, 2}, {703, 1, 3}, {704, 1, 4}, {711, 2, 1}, {712, 2, 2},
	} {
		if _, err := st.EnsureMediaItem(ctx, store.MediaItem{RequestID: &tvReq, MediaType: "episode",
			TvdbID: i64(100), SonarrSeriesID: i64(7), SonarrEpisodeID: i64(ep.id),
			SeasonNumber: i64(ep.season), EpisodeNumber: i64(ep.episode), Title: "Series"}); err != nil {
			t.Fatal(err)
		}
	}
	request("tv", "Never Added", nil, i64(200))

	rep, err := a.RecheckOpen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("errors: %v", rep.Errors)
	}
	want := RecheckReport{Open: 8, Missing: 6, Reset: 1, Reconciled: 1, Waiting: 2, Downloading: 1,
		Unmonitored: 1, NotInArr: 2}
	got := *rep
	got.Requests, got.SearchedAt = nil, nil
	if got.Open != want.Open || got.Missing != want.Missing || got.Reset != want.Reset ||
		got.Reconciled != want.Reconciled || got.Waiting != want.Waiting ||
		got.Downloading != want.Downloading || got.Unmonitored != want.Unmonitored ||
		got.NotInArr != want.NotInArr || got.SearchSkipped {
		t.Fatalf("report = %+v, want %+v", got, want)
	}
	outcomes := map[string]string{}
	for _, r := range rep.Requests {
		outcomes[r.Title] = r.Outcome
	}
	for title, o := range map[string]string{"On Disk": outReconciled, "Failed": outReset, "Not Out": outWaiting,
		"Unknown": outNotInArr, "Running": outDownloading, "Unmonitored": outUnmonitored,
		"Series": outMissing, "Never Added": outNotInArr} {
		if outcomes[title] != o {
			t.Errorf("%s: outcome %q, want %q", title, outcomes[title], o)
		}
	}

	// The file on disk moved the item to imported; the failed attempt is a
	// new one.
	if it, _ := st.GetMediaItem(ctx, onDisk); it.CurrentStage != "imported" {
		t.Errorf("on-disk item at %q, want imported", it.CurrentStage)
	}
	if it, _ := st.GetMediaItem(ctx, failed); it.CurrentCycle != 2 || it.CurrentStage != "requested" {
		t.Errorf("failed item at cycle %d stage %q, want a new attempt", it.CurrentCycle, it.CurrentStage)
	}
	if r, _ := st.GetRequest(ctx, onDiskReq); r.Status != "active" {
		t.Errorf("request status %q (imported is not yet available)", r.Status)
	}

	// Radarr: one search for the failed film only. Sonarr: season 1 at once
	// (four episodes), season 2 episode by episode.
	rc := radarr.sent()
	if len(rc) != 1 || rc[0]["name"] != "MoviesSearch" || !sameIDs(rc[0]["movieIds"], 12) {
		t.Errorf("radarr commands = %v", rc)
	}
	sc := sonarr.sent()
	if len(sc) != 2 || sc[0]["name"] != "SeasonSearch" || sc[0]["seasonNumber"] != float64(1) ||
		sc[1]["name"] != "EpisodeSearch" || !sameIDs(sc[1]["episodeIds"], 711) {
		t.Errorf("sonarr commands = %v", sc)
	}

	// Right after: the same findings, but no second search.
	rep2, err := a.RecheckOpen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.SearchSkipped || rep2.SearchedAt == nil || !rep2.SearchedAt.Equal(now) {
		t.Errorf("second run: skipped=%v at=%v, want skipped at %v", rep2.SearchSkipped, rep2.SearchedAt, now)
	}
	if len(radarr.sent()) != 1 || len(sonarr.sent()) != 2 {
		t.Errorf("second run searched again: radarr %v sonarr %v", radarr.sent(), sonarr.sent())
	}

	// After the cooldown it searches again.
	now = now.Add(RecheckSearchCooldown + time.Minute)
	if _, err := a.RecheckOpen(ctx); err != nil {
		t.Fatal(err)
	}
	if len(radarr.sent()) != 2 {
		t.Errorf("after the cooldown: radarr commands %v", radarr.sent())
	}

	// The audit row carries the summary.
	audit, err := st.RecentActions(ctx, 1)
	if err != nil || len(audit) != 1 || audit[0].Kind != "recheck_open" || audit[0].Status != "ok" ||
		audit[0].Detail == "" {
		t.Fatalf("audit = %+v, %v", audit, err)
	}
}

func sameIDs(v any, want ...int64) bool {
	got, ok := v.([]any)
	if !ok || len(got) != len(want) {
		return false
	}
	for i, g := range got {
		if f, ok := g.(float64); !ok || int64(f) != want[i] {
			return false
		}
	}
	return true
}

func TestRecheckOfferedWithAnArr(t *testing.T) {
	reg, err := registry.Build(nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := &Actions{Store: newTestStore(t), Reg: reg}
	if hasKind(a.Available(context.Background(), "global", 0), "recheck-open") {
		t.Fatal("recheck-open offered without Sonarr/Radarr")
	}
	a.Radarr = &clients.Arr{}
	if !hasKind(a.Available(context.Background(), "global", 0), "recheck-open") {
		t.Fatal("recheck-open not offered with Radarr")
	}
}
