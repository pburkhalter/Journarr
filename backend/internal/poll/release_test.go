package poll

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

type fakeRadarr struct {
	mu    sync.Mutex
	movie map[string]any
}

func (f *fakeRadarr) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/api/v3/movie" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{f.movie})
	}
}

func (f *fakeRadarr) set(k string, v any) {
	f.mu.Lock()
	f.movie[k] = v
	f.mu.Unlock()
}

// trackedMovie creates an active movie request whose item sits at approved.
func trackedMovie(t *testing.T, st *store.Store, tmdb int64) int64 {
	t.Helper()
	ctx := context.Background()
	seerrID := tmdb + 5000
	reqID, err := st.UpsertRequest(ctx, store.Request{
		SeerrRequestID: &seerrID, MediaType: "movie", TmdbID: i64(tmdb), Title: "Film",
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := st.EnsureMediaItem(ctx, store.MediaItem{
		RequestID: &reqID, MediaType: "movie", TmdbID: i64(tmdb), Title: "Film",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStage(ctx, itemID, 1, "approved", 0, ""); err != nil {
		t.Fatal(err)
	}
	return itemID
}

func awaiting(t *testing.T, st *store.Store, itemID int64) *time.Time {
	t.Helper()
	it, err := st.GetMediaItem(context.Background(), itemID)
	if err != nil || it == nil {
		t.Fatal("item missing")
	}
	return it.AwaitingReleaseAt
}

// With minimumAvailability=announced Radarr reports every film as available
// the moment it is added. The poller used to clear the waiting flag on that
// alone, so an unreleased film read as stuck. The release date must win.
func TestReleasePollerWaitsOnFutureDateDespiteIsAvailable(t *testing.T) {
	st := newTestStore(t)
	itemID := trackedMovie(t, st, 42)
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	digital := now.Add(5 * 24 * time.Hour)

	fake := &fakeRadarr{movie: map[string]any{
		"tmdbId": 42, "isAvailable": true, "hasFile": false,
		"inCinemas": now.Add(-30 * 24 * time.Hour), "digitalRelease": digital,
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	p := &ReleasePoller{Store: st, Log: quietLog(),
		Radarr: clients.NewArr("radarr", srv.URL, "/api/v3", "k", 0),
		Now:    func() time.Time { return now }}
	p.pass(context.Background())

	got := awaiting(t, st, itemID)
	if got == nil || got.Format("2006-01-02") != digital.Format("2006-01-02") {
		t.Fatalf("awaiting = %v, want the digital date %s", got, digital.Format("2006-01-02"))
	}

	// The file lands: nothing to wait for any more.
	fake.set("hasFile", true)
	p.pass(context.Background())
	if got := awaiting(t, st, itemID); got != nil {
		t.Fatalf("awaiting = %v after hasFile, want nil", got)
	}
}

func TestReleasePollerSentinelWhenNoDates(t *testing.T) {
	st := newTestStore(t)
	itemID := trackedMovie(t, st, 43)
	fake := &fakeRadarr{movie: map[string]any{"tmdbId": 43, "isAvailable": true, "hasFile": false}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	p := &ReleasePoller{Store: st, Log: quietLog(), Radarr: clients.NewArr("radarr", srv.URL, "/api/v3", "k", 0)}
	p.pass(context.Background())

	got := awaiting(t, st, itemID)
	if got == nil || got.Year() != 9999 {
		t.Fatalf("awaiting = %v, want the TBA sentinel", got)
	}
}

// A film that is out (dates in the past) and searchable is simply missing —
// that is the case retry and stuck detection exist for, not a waiting state.
func TestReleasePollerDoesNotWaitOnReleasedFilm(t *testing.T) {
	st := newTestStore(t)
	itemID := trackedMovie(t, st, 44)
	now := time.Now()
	fake := &fakeRadarr{movie: map[string]any{
		"tmdbId": 44, "isAvailable": true, "hasFile": false,
		"digitalRelease": now.Add(-10 * 24 * time.Hour),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	if err := st.SetAwaitingRelease(context.Background(), itemID, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	p := &ReleasePoller{Store: st, Log: quietLog(), Radarr: clients.NewArr("radarr", srv.URL, "/api/v3", "k", 0)}
	p.pass(context.Background())

	if got := awaiting(t, st, itemID); got != nil {
		t.Fatalf("awaiting = %v for a released film, want nil", got)
	}
}
