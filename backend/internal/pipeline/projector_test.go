package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/pburkhalter/journarr/internal/store"
)

func testProjector(t *testing.T) (*Projector, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := New(s, slog.Default(), nil, nil, nil)
	return p, s
}

func emit(t *testing.T, s *store.Store, source, kind string, op any) {
	t.Helper()
	payload, _ := json.Marshal(op)
	if _, _, err := s.InsertEvent(context.Background(), source, kind, "", payload); err != nil {
		t.Fatal(err)
	}
}

func TestMovieLifecycle(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	// 1. Seerr approval creates request + movie item at 'approved'.
	emit(t, s, "seerr", "approved", SeerrOp{
		SeerrRequestID: 7, Kind: "approved", MediaType: "movie",
		TmdbID: 693134, Title: "Dune: Part Two", Year: 2024, RequestedBy: "patrik",
	})
	p.drain(ctx)

	req, err := s.FindRequestBySeerrID(ctx, 7)
	if err != nil || req == nil {
		t.Fatalf("request missing: %v", err)
	}
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if len(items) != 1 || items[0].CurrentStage != "approved" {
		t.Fatalf("want 1 item @approved, got %+v", items)
	}

	// 2. Radarr grab → 'grabbed' + download row.
	emit(t, s, "radarr", "grab", GrabOp{
		Arr: "radarr", DownloadID: "ABCDEF999", ReleaseTitle: "Dune.Part.Two.2024.1080p",
		Protocol: "usenet",
		Movie:    &MovieRef{RadarrID: 686, TmdbID: 693134, Title: "Dune: Part Two"},
	})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if len(items) != 1 {
		t.Fatalf("movie must stay a single item (NULL-dedupe regression), got %d", len(items))
	}
	if items[0].CurrentStage != "grabbed" {
		t.Fatalf("want grabbed, got %s", items[0].CurrentStage)
	}
	dl, _ := s.FindDownloadByClientID(ctx, "abcdef999")
	if dl == nil || dl.State != "grabbed" {
		t.Fatalf("download row missing/wrong: %+v", dl)
	}

	// 3. Duplicate grab (webhook + history poller) — no new cycle.
	emit(t, s, "radarr", "grab", GrabOp{
		Arr: "radarr", DownloadID: "abcdef999",
		Movie: &MovieRef{RadarrID: 686, TmdbID: 693134, Title: "Dune: Part Two"},
	})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentCycle != 1 {
		t.Fatalf("duplicate grab bumped cycle: %d", items[0].CurrentCycle)
	}

	// 4. Import → 'imported', download imported, path recorded.
	emit(t, s, "radarr", "import", ImportOp{
		Arr: "radarr", DownloadID: "ABCDEF999",
		Movie:     &MovieRef{RadarrID: 686, TmdbID: 693134, Title: "Dune: Part Two"},
		MoviePath: "/media/movies/Dune Part Two (2024)/movie.mkv",
	})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if len(items) != 1 {
		t.Fatalf("movie item duplicated on import, got %d", len(items))
	}
	if items[0].CurrentStage != "imported" || items[0].ImportedPath == "" {
		t.Fatalf("want imported+path, got %+v", items[0])
	}
	dl, _ = s.FindDownloadByClientID(ctx, "abcdef999")
	if dl.State != "imported" {
		t.Fatalf("download not imported: %s", dl.State)
	}

	// 5. Seerr available → request completed.
	emit(t, s, "seerr", "available", SeerrOp{
		SeerrRequestID: 7, Kind: "available", MediaType: "movie", TmdbID: 693134,
	})
	p.drain(ctx)
	req, _ = s.FindRequestBySeerrID(ctx, 7)
	if req.Status != "completed" {
		t.Fatalf("want completed, got %s", req.Status)
	}
}

func TestRegrabStartsNewCycle(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	emit(t, s, "seerr", "approved", SeerrOp{
		SeerrRequestID: 8, Kind: "approved", MediaType: "movie", TmdbID: 100, Title: "Film",
	})
	emit(t, s, "radarr", "grab", GrabOp{
		Arr: "radarr", DownloadID: "dl-one",
		Movie: &MovieRef{RadarrID: 1, TmdbID: 100, Title: "Film"},
	})
	p.drain(ctx)

	// Failure, then a re-grab with a different download id → cycle 2.
	emit(t, s, "radarr", "failure", FailureOp{
		Arr: "radarr", DownloadID: "dl-one", Message: "TorBox: missing_files",
		Movie: &MovieRef{RadarrID: 1, TmdbID: 100, Title: "Film"},
	})
	emit(t, s, "radarr", "grab", GrabOp{
		Arr: "radarr", DownloadID: "dl-two",
		Movie: &MovieRef{RadarrID: 1, TmdbID: 100, Title: "Film"},
	})
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 8)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentCycle != 2 || items[0].CurrentStage != "grabbed" {
		t.Fatalf("want grabbed/2 after regrab, got %s/%d", items[0].CurrentStage, items[0].CurrentCycle)
	}
	first, _ := s.FindDownloadByClientID(ctx, "dl-one")
	if first.State != "failed" || first.LastError == "" {
		t.Fatalf("first download should be failed: %+v", first)
	}
}

func TestLateEventsForSupersededDownloadStayInTheirCycle(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	movie := &MovieRef{RadarrID: 1, TmdbID: 100, Title: "Film"}
	emit(t, s, "seerr", "approved", SeerrOp{
		SeerrRequestID: 9, Kind: "approved", MediaType: "movie", TmdbID: 100, Title: "Film",
	})
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "dl-one", Movie: movie})
	emit(t, s, "radarr", "failure", FailureOp{Arr: "radarr", DownloadID: "dl-one", Message: "boom", Movie: movie})
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "dl-two", Movie: movie})
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 9)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentCycle != 2 || items[0].CurrentStage != "grabbed" {
		t.Fatalf("setup: want grabbed/2, got %s/%d", items[0].CurrentStage, items[0].CurrentCycle)
	}

	// Late replay of the OLD grab (history poller lag) — must not bump a
	// third cycle, must not move the pointer.
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "dl-one", Movie: movie})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentCycle != 2 || items[0].CurrentStage != "grabbed" {
		t.Fatalf("late grab replay corrupted state: %s/%d", items[0].CurrentStage, items[0].CurrentCycle)
	}

	// Late import of the OLD download lands in cycle 1 and must not advance
	// the current cycle's pointer past 'grabbed'.
	emit(t, s, "radarr", "import", ImportOp{
		Arr: "radarr", DownloadID: "dl-one", Movie: movie, MoviePath: "/old/path.mkv",
	})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "grabbed" || items[0].CurrentCycle != 2 {
		t.Fatalf("late import advanced current cycle: %s/%d", items[0].CurrentStage, items[0].CurrentCycle)
	}
	if items[0].ImportedPath != "" {
		t.Fatalf("late import wrote path onto current cycle: %q", items[0].ImportedPath)
	}

	// A stale failure of dl-one must not smear an error over cycle 2.
	_ = s.SetItemError(ctx, items[0].ID, "") // reset
	emit(t, s, "radarr", "failure", FailureOp{Arr: "radarr", DownloadID: "dl-one", Message: "stale", Movie: movie})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].LastError == "stale" {
		t.Fatal("stale failure of superseded download smeared error over new cycle")
	}

	// The real import for dl-two completes cycle 2.
	emit(t, s, "radarr", "import", ImportOp{
		Arr: "radarr", DownloadID: "dl-two", Movie: movie, MoviePath: "/new/path.mkv",
	})
	p.drain(ctx)
	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "imported" || items[0].ImportedPath != "/new/path.mkv" {
		t.Fatalf("real import failed: %s path=%q", items[0].CurrentStage, items[0].ImportedPath)
	}
}

func TestOrphanGrabCreatesRequest(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	// A grab with no Seerr request behind it → orphan request bucket.
	emit(t, s, "sonarr", "grab", GrabOp{
		Arr: "sonarr", DownloadID: "hash123",
		Series: &SeriesRef{SonarrID: 5, TvdbID: 999, Title: "Manual Show"},
		Episodes: []EpisodeRef{
			{SonarrID: 51, Season: 1, Episode: 1, Title: "One"},
			{SonarrID: 52, Season: 1, Episode: 2, Title: "Two"},
		},
	})
	p.drain(ctx)

	list, err := s.ListRequests(ctx, "active", "", 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("want 1 orphan request, got %d err=%v", len(list), err)
	}
	if list[0].SeerrRequestID != nil || list[0].ItemCount != 2 {
		t.Fatalf("orphan shape wrong: %+v", list[0])
	}
	if list[0].StageCounts["grabbed"] != 2 {
		t.Fatalf("want 2 grabbed, got %+v", list[0].StageCounts)
	}

	// Season-pack import: both episodes flip, download becomes imported.
	emit(t, s, "sonarr", "import", ImportOp{
		Arr: "sonarr", DownloadID: "HASH123",
		Series: &SeriesRef{SonarrID: 5, TvdbID: 999, Title: "Manual Show"},
		Episodes: []EpisodeRef{
			{SonarrID: 51, Season: 1, Episode: 1},
			{SonarrID: 52, Season: 1, Episode: 2},
		},
	})
	p.drain(ctx)
	dl, _ := s.FindDownloadByClientID(ctx, "hash123")
	if dl.State != "imported" {
		t.Fatalf("season pack download not imported: %s", dl.State)
	}
}
