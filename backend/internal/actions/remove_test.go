package actions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// fakeStack plays Sonarr, Seerr and Jellyfin on one server and records the
// calls that change something.
type fakeStack struct {
	mu        sync.Mutex
	calls     []string
	watching  bool
	deleted   bool // series gone from Sonarr
	failArr   bool
	jellyLeft bool
}

func (f *fakeStack) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeStack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series" && q.Get("tvdbId") == "555":
		if f.deleted {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, `[{"id":9,"title":"Show","tvdbId":555,"path":"/media/tv/Show",
			"statistics":{"seasonCount":2,"episodeFileCount":20,"sizeOnDisk":21474836480}}]`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
		if f.deleted {
			_, _ = io.WriteString(w, `{"records":[],"totalRecords":0}`)
			return
		}
		// a season pack (two rows, one download) plus another series' download
		_, _ = io.WriteString(w, `{"records":[
			{"id":71,"downloadId":"arrarr_a","title":"Show.S02","seriesId":9,"episodeId":1},
			{"id":72,"downloadId":"arrarr_a","title":"Show.S02","seriesId":9,"episodeId":2},
			{"id":80,"downloadId":"arrarr_b","title":"Other.S01E01","seriesId":10,"episodeId":5}],"totalRecords":3}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/queue/"):
		f.record("DELETE " + r.URL.Path + "?" + r.URL.RawQuery)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/series/9":
		if f.failArr {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.record("DELETE " + r.URL.Path + "?" + r.URL.RawQuery)
		f.deleted = true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tv/1399":
		_, _ = io.WriteString(w, `{"name":"Show","mediaInfo":{"id":314}}`)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/media/314":
		f.record("DELETE " + r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/Sessions":
		if f.watching {
			_, _ = io.WriteString(w, `[{"UserName":"adi","NowPlayingItem":{"Name":"Pilot","Type":"Episode",
				"SeriesName":"Show","Path":"/media/tv/Show/Season 1/Show.S01E01.mkv"}}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case r.URL.Path == "/Items":
		if f.jellyLeft {
			_, _ = io.WriteString(w, `{"Items":[{"Id":"x","Name":"Show","Type":"Series","Path":"/media/tv/Show"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"Items":[]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/Library/Refresh":
		f.record("POST /Library/Refresh")
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func setupRemoval(t *testing.T) (*Actions, *store.Store, *fakeStack, int64) {
	t.Helper()
	removeSettle = 0
	st := newTestStore(t)
	ctx := context.Background()
	f := &fakeStack{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	hc := &http.Client{Timeout: 5 * time.Second}
	a := &Actions{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sonarr: &clients.Arr{Name: "sonarr", BaseURL: srv.URL, APIBase: "/api/v3", APIKey: "k", HTTP: hc},
		Seerr:  &clients.Seerr{BaseURL: srv.URL, HTTP: hc},
		Jelly:  &clients.Jellyfin{BaseURL: srv.URL, HTTP: hc},
	}
	seerrID, tmdb, tvdb := int64(77), int64(1399), int64(555)
	reqID, err := st.UpsertRequest(ctx, store.Request{SeerrRequestID: &seerrID, MediaType: "tv",
		TmdbID: &tmdb, TvdbID: &tvdb, Title: "Show"})
	if err != nil {
		t.Fatal(err)
	}
	sid := int64(9)
	if _, err := st.EnsureMediaItem(ctx, store.MediaItem{RequestID: &reqID, MediaType: "episode",
		TvdbID: &tvdb, SonarrSeriesID: &sid, SeasonNumber: &[]int64{1}[0], EpisodeNumber: &[]int64{1}[0]}); err != nil {
		t.Fatal(err)
	}
	return a, st, f, reqID
}

func TestRemoveDeletesTitleEverywhere(t *testing.T) {
	a, st, f, reqID := setupRemoval(t)
	ctx := context.Background()

	plan, err := a.PlanRemoval(ctx, reqID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ArrID != 9 || plan.Files != 20 || len(plan.Downloads) != 1 || !plan.InSeerr {
		t.Fatalf("plan = %+v", plan)
	}

	if _, err := a.Remove(ctx, reqID, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		// one delete per download, with the data, no blocklist
		"DELETE /api/v3/queue/71?removeFromClient=true&blocklist=false",
		"DELETE /api/v3/series/9?deleteFiles=true&addImportListExclusion=false",
		"DELETE /api/v1/media/314",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if r, _ := st.GetRequest(ctx, reqID); r != nil {
		t.Fatal("Journarr request still there")
	}
	if !st.IsArrRemoved(ctx, "sonarr", 9) || !st.IsSeerrRequestRemoved(ctx, 77) {
		t.Fatal("tombstone missing")
	}
}

func TestRemoveRefusesWhileSomeoneWatches(t *testing.T) {
	a, st, f, reqID := setupRemoval(t)
	f.watching = true
	ctx := context.Background()

	if _, err := a.Remove(ctx, reqID, false); !errors.Is(err, ErrBeingWatched) {
		t.Fatalf("err = %v, want ErrBeingWatched", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("changed something while refusing: %v", f.calls)
	}
	if _, err := a.Remove(ctx, reqID, true); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
	if r, _ := st.GetRequest(ctx, reqID); r != nil {
		t.Fatal("forced remove left the request")
	}
}

// A failed Sonarr delete stops the cascade: Seerr and Journarr still show the
// title so a second click can finish the job.
func TestRemoveStopsWhenArrDeleteFails(t *testing.T) {
	a, st, f, reqID := setupRemoval(t)
	f.failArr = true
	ctx := context.Background()

	steps, err := a.Remove(ctx, reqID, false)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "/api/v1/media/") {
			t.Fatal("Seerr was changed after Sonarr failed")
		}
	}
	if r, _ := st.GetRequest(ctx, reqID); r == nil {
		t.Fatal("Journarr request purged after Sonarr failed")
	}
	if last := steps[len(steps)-1]; last.Step != "sonarr" || last.Status != "failed" {
		t.Fatalf("last step = %+v", last)
	}

	// retry after Sonarr recovers finishes the job
	f.failArr = false
	if _, err := a.Remove(ctx, reqID, false); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.GetRequest(ctx, reqID); r != nil {
		t.Fatal("second click did not finish the removal")
	}
}

// Jellyfin normally drops the title on Sonarr's notification; when it is
// still listed, a library scan is started.
func TestRemoveScansJellyfinWhenTitleLingers(t *testing.T) {
	a, _, f, reqID := setupRemoval(t)
	f.jellyLeft = true
	steps, err := a.Remove(context.Background(), reqID, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls[len(f.calls)-1] != "POST /Library/Refresh" {
		t.Fatalf("no library scan: %v", f.calls)
	}
	b, _ := json.Marshal(steps)
	if !strings.Contains(string(b), "library scan started") {
		t.Fatalf("steps = %s", b)
	}
}
