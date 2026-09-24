package poll

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/store"
)

// fakeJellyfin mimics Jellyfin 10.11 reached with an api key and no user
// context: the single-item route /Items/{id} answers 400, the collection
// route /Items works (including Ids= lookups and StartIndex paging).
type fakeJellyfin struct {
	mu     sync.Mutex
	items  []map[string]any // newest first
	series map[string]string
	pages  int
}

func (f *fakeJellyfin) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query()
		switch {
		case strings.HasPrefix(r.URL.Path, "/Items/"):
			http.Error(w, `{"title":"Bad Request"}`, http.StatusBadRequest)
		case r.URL.Path == "/Items" && q.Get("Ids") != "":
			tvdb, ok := f.series[q.Get("Ids")]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{
				map[string]any{"Id": q.Get("Ids"), "ProviderIds": map[string]string{"Tvdb": tvdb}},
			}})
		case r.URL.Path == "/Items":
			f.pages++
			start, limit := atoi(q.Get("StartIndex")), atoi(q.Get("Limit"))
			end := start + limit
			if end > len(f.items) {
				end = len(f.items)
			}
			page := []map[string]any{}
			if start < end {
				page = f.items[start:end]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": page})
		default:
			http.NotFound(w, r)
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func episode(id, seriesID string, season, ep int64, created time.Time) map[string]any {
	return map[string]any{
		"Id": id, "Type": "Episode", "SeriesId": seriesID,
		"ParentIndexNumber": season, "IndexNumber": ep,
		"DateCreated": created.UTC().Format("2006-01-02T15:04:05.0000000Z"),
	}
}

// trackedEpisode creates an active tv request with one imported episode.
func trackedEpisode(t *testing.T, st *store.Store, tvdb, season, ep int64) int64 {
	t.Helper()
	ctx := context.Background()
	seerrID := tvdb + 1000
	reqID, err := st.UpsertRequest(ctx, store.Request{
		SeerrRequestID: &seerrID, MediaType: "tv", TvdbID: i64(tvdb), Title: "Show",
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := st.EnsureMediaItem(ctx, store.MediaItem{
		RequestID: &reqID, MediaType: "episode", TvdbID: i64(tvdb),
		SeasonNumber: i64(season), EpisodeNumber: i64(ep), Title: "Ep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStage(ctx, itemID, 1, "imported", 0, ""); err != nil {
		t.Fatal(err)
	}
	return itemID
}

func availableEvents(t *testing.T, st *store.Store) []int64 {
	t.Helper()
	evs, err := st.FetchUnprocessed(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, ev := range evs {
		if ev.Source != "jellyfin" {
			continue
		}
		var op pipeline.AvailableOp
		_ = json.Unmarshal(ev.Payload, &op)
		ids = append(ids, op.MediaItemID)
	}
	return ids
}

// Jellyfin 10.11 answers 400 to /Items/{id} without a user context. The
// poller looked the series up that way, logged the failure at debug level and
// skipped every episode — for months, availability was never observed here.
func TestJellyfinPollerMatchesEpisodesWithoutUserContext(t *testing.T) {
	st := newTestStore(t)
	itemID := trackedEpisode(t, st, 12345, 1, 3)

	fake := &fakeJellyfin{series: map[string]string{"ser1": "12345"}}
	fake.items = []map[string]any{episode("ep1", "ser1", 1, 3, time.Now())}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	p := &JellyfinPoller{Store: st, Log: quietLog(), Jelly: clients.NewJellyfin(srv.URL, "key", 0)}
	p.pass(context.Background())

	got := availableEvents(t, st)
	if len(got) != 1 || got[0] != itemID {
		t.Fatalf("available events = %v, want [%d]", got, itemID)
	}
	if cur, _ := st.GetPollCursor(context.Background(), jellyfinCursorKey); cur == "" {
		t.Fatal("cursor not saved after a pass")
	}
}

// DateCreated is the file's creation time, not the library-add time. A slow
// import can surface with an older stamp than a faster one already seen, so
// the cursor must not be a hard floor.
func TestJellyfinPollerCatchesItemsOlderThanCursor(t *testing.T) {
	st := newTestStore(t)
	fast := trackedEpisode(t, st, 111, 1, 1)
	slow := trackedEpisode(t, st, 222, 2, 2)

	now := time.Now()
	fake := &fakeJellyfin{series: map[string]string{"s1": "111", "s2": "222"}}
	fake.items = []map[string]any{episode("e1", "s1", 1, 1, now)}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	p := &JellyfinPoller{Store: st, Log: quietLog(), Jelly: clients.NewJellyfin(srv.URL, "key", 0)}
	p.pass(context.Background())

	// The slower import appears later but with an older DateCreated.
	fake.mu.Lock()
	fake.items = []map[string]any{
		episode("e1", "s1", 1, 1, now),
		episode("e2", "s2", 2, 2, now.Add(-time.Hour)),
	}
	fake.mu.Unlock()
	p.pass(context.Background())

	got := availableEvents(t, st)
	if len(got) != 2 || got[0] != fast || got[1] != slow {
		t.Fatalf("available events = %v, want [%d %d]", got, fast, slow)
	}
}

// With a cursor the walk stops once a page reaches past the overlap window
// instead of paging through the whole library.
func TestJellyfinPollerStopsPagingBehindTheCursor(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	fake := &fakeJellyfin{series: map[string]string{}}
	// 350 old items far behind any cursor, one fresh item on top.
	fake.items = append(fake.items, episode("fresh", "sX", 1, 1, now))
	for i := 0; i < 350; i++ {
		fake.items = append(fake.items, episode("old", "sX", 1, 1, now.Add(-72*time.Hour)))
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	if err := st.SetPollCursor(context.Background(), jellyfinCursorKey, now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	p := &JellyfinPoller{Store: st, Log: quietLog(), Jelly: clients.NewJellyfin(srv.URL, "key", 0)}
	p.pass(context.Background())

	if fake.pages != 1 {
		t.Fatalf("fetched %d pages, want 1 — the overlap floor should stop the walk", fake.pages)
	}
}
