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

// emitDedupe mirrors a poller emitting with a natural dedupe key; returns
// whether the event was newly inserted (false = deduped).
func emitDedupe(t *testing.T, s *store.Store, source, kind, dedupe string, op any) bool {
	t.Helper()
	payload, _ := json.Marshal(op)
	_, inserted, err := s.InsertEvent(context.Background(), source, kind, dedupe, payload)
	if err != nil {
		t.Fatal(err)
	}
	return inserted
}

// Reachable stuck scenario: a TV episode goes available while the series
// request is still active (other episodes pending), then that episode is
// re-grabbed as an upgrade (cycle 2). With the old item-only availability
// dedupe key, cycle-2 availability could never be re-emitted and the episode
// stayed stuck below 'available'. The cycle-scoped key fixes it.
func TestEpisodeAvailabilityRecoversAcrossUpgradeCycle(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	series := &SeriesRef{SonarrID: 9, TvdbID: 555, Title: "Show"}
	// Season pack grab creates two episode items under one orphan request.
	emit(t, s, "sonarr", "grab", GrabOp{Arr: "sonarr", DownloadID: "pack-a", Series: series, Episodes: []EpisodeRef{
		{SonarrID: 1, Season: 1, Episode: 1, Title: "E1"},
		{SonarrID: 2, Season: 1, Episode: 2, Title: "E2"},
	}})
	emit(t, s, "sonarr", "import", ImportOp{Arr: "sonarr", DownloadID: "pack-a", Series: series, Episodes: []EpisodeRef{
		{SonarrID: 1, Season: 1, Episode: 1},
	}})
	p.drain(ctx)

	list, _ := s.ListRequests(ctx, "active", "", 10, 0)
	reqID := list[0].ID
	items, _ := s.ListItemsForRequest(ctx, reqID)
	var e1 *store.MediaItem
	for i := range items {
		if items[i].EpisodeNumber != nil && *items[i].EpisodeNumber == 1 {
			e1 = &items[i]
		}
	}
	if e1 == nil {
		t.Fatal("E1 not found")
	}

	// E1 available in cycle 1; request stays active (E2 pending).
	emitDedupe(t, s, "jellyfin", "available", "jellyfin:avail:"+itoa64(e1.ID)+":1", AvailableOp{MediaItemID: e1.ID, JellyfinItemID: "jf1"})
	p.drain(ctx)
	if req, _ := s.GetRequest(ctx, reqID); req.Status != "active" {
		t.Fatalf("request should still be active (E2 pending), got %s", req.Status)
	}

	// Upgrade re-grab of E1 with a different download → cycle 2.
	emit(t, s, "sonarr", "grab", GrabOp{Arr: "sonarr", DownloadID: "e1-b", Series: series, Episodes: []EpisodeRef{
		{SonarrID: 1, Season: 1, Episode: 1},
	}})
	emit(t, s, "sonarr", "import", ImportOp{Arr: "sonarr", DownloadID: "e1-b", Series: series, Episodes: []EpisodeRef{
		{SonarrID: 1, Season: 1, Episode: 1},
	}})
	p.drain(ctx)
	if got, _ := s.GetMediaItem(ctx, e1.ID); got.CurrentCycle != 2 || got.CurrentStage != "imported" {
		t.Fatalf("upgrade: want imported/2, got %s/%d", got.CurrentStage, got.CurrentCycle)
	}

	// Old cycle-1 key stays deduped; cycle-2 key is fresh → availability recovers.
	if emitDedupe(t, s, "jellyfin", "available", "jellyfin:avail:"+itoa64(e1.ID)+":1", AvailableOp{MediaItemID: e1.ID}) {
		t.Fatal("cycle-1 key should still dedupe")
	}
	if !emitDedupe(t, s, "jellyfin", "available", "jellyfin:avail:"+itoa64(e1.ID)+":2", AvailableOp{MediaItemID: e1.ID, JellyfinItemID: "jf2"}) {
		t.Fatal("cycle-2 key should insert")
	}
	p.drain(ctx)
	if got, _ := s.GetMediaItem(ctx, e1.ID); got.CurrentStage != "available" {
		t.Fatalf("cycle 2: want available, got %s", got.CurrentStage)
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

func TestArrarrSubstagesAndAvailable(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	movie := &MovieRef{RadarrID: 5, TmdbID: 693134, Title: "Dune"}
	emit(t, s, "seerr", "approved", SeerrOp{
		SeerrRequestID: 3, Kind: "approved", MediaType: "movie", TmdbID: 693134, Title: "Dune",
	})
	// Grab via arrarr nzo id (SAB path: downloadId == nzo_id).
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "arrarr_abc", Movie: movie})
	p.drain(ctx)

	// Arrarr TorBox substages, correlated by nzo_id.
	for _, to := range []string{"SUBMITTED", "DOWNLOADING", "COMPLETED_TORBOX", "READY"} {
		emit(t, s, "arrarr", "job.transition", JobTransitionOp{NzoID: "arrarr_abc", To: to})
	}
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 3)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "downloaded" {
		t.Fatalf("want downloaded after READY, got %s", items[0].CurrentStage)
	}
	dl, _ := s.FindDownloadByClientID(ctx, "arrarr_abc")
	if dl.State != "downloaded" || dl.ArrarrNzoID != "arrarr_abc" {
		t.Fatalf("download not tracked: state=%s nzo=%s", dl.State, dl.ArrarrNzoID)
	}

	// Import from Radarr, then Jellyfin availability.
	emit(t, s, "radarr", "import", ImportOp{Arr: "radarr", DownloadID: "arrarr_abc", Movie: movie, MoviePath: "/m/dune.mkv"})
	p.drain(ctx)
	emit(t, s, "jellyfin", "available", AvailableOp{MediaItemID: items[0].ID, JellyfinItemID: "jf123"})
	p.drain(ctx)

	items, _ = s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "available" || items[0].JellyfinItemID != "jf123" {
		t.Fatalf("want available+jellyfin id, got %s / %q", items[0].CurrentStage, items[0].JellyfinItemID)
	}
	req, _ = s.FindRequestBySeerrID(ctx, 3)
	if req.Status != "completed" {
		t.Fatalf("request should be completed, got %s", req.Status)
	}
}

func TestNotifiedStage(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	// Movie: notified matched by tmdb, even after the request completed.
	movie := &MovieRef{RadarrID: 1, TmdbID: 693134, Title: "Dune"}
	emit(t, s, "seerr", "approved", SeerrOp{SeerrRequestID: 1, Kind: "approved", MediaType: "movie", TmdbID: 693134, Title: "Dune"})
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "d1", Movie: movie})
	emit(t, s, "radarr", "import", ImportOp{Arr: "radarr", DownloadID: "d1", Movie: movie, MoviePath: "/d.mkv"})
	emit(t, s, "concierge", "notified", NotifiedOp{MediaType: "movie", TmdbID: 693134, Title: "Dune"})
	p.drain(ctx)
	req, _ := s.FindRequestBySeerrID(ctx, 1)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "notified" {
		t.Fatalf("movie: want notified, got %s", items[0].CurrentStage)
	}
	if req.Status != "completed" {
		t.Fatalf("movie: notified should complete the request, got %s", req.Status)
	}

	// TV: notification carries the series TMDB id; episodes matched via the
	// request's tmdb_id (items are keyed by tvdb).
	emit(t, s, "seerr", "approved", SeerrOp{SeerrRequestID: 2, Kind: "approved", MediaType: "tv", TmdbID: 95396, TvdbID: 371980, Title: "Sev"})
	// no fanout (no sonarr client in test) — episodes appear at grab time
	emit(t, s, "sonarr", "grab", GrabOp{Arr: "sonarr", DownloadID: "s1",
		Series:   &SeriesRef{SonarrID: 1, TvdbID: 371980, TmdbID: 95396, Title: "Sev"},
		Episodes: []EpisodeRef{{SonarrID: 10, Season: 1, Episode: 1}, {SonarrID: 11, Season: 1, Episode: 2}}})
	p.drain(ctx)
	// The grab attaches to the active seerr tv request (matched by tvdb).
	emit(t, s, "concierge", "notified", NotifiedOp{MediaType: "tv", TmdbID: 95396, Title: "Sev",
		Episodes: []EpisodeNum{{Season: 1, Episode: 1}}})
	p.drain(ctx)
	req2, _ := s.FindRequestBySeerrID(ctx, 2)
	items2, _ := s.ListItemsForRequest(ctx, req2.ID)
	var e1, e2 *store.MediaItem
	for i := range items2 {
		if *items2[i].EpisodeNumber == 1 {
			e1 = &items2[i]
		} else {
			e2 = &items2[i]
		}
	}
	if e1 == nil || e1.CurrentStage != "notified" {
		t.Fatalf("tv E1: want notified, got %v", e1)
	}
	if e2 == nil || e2.CurrentStage == "notified" {
		t.Fatalf("tv E2: should NOT be notified (not in the batch), got %v", e2 != nil && e2.CurrentStage == "notified")
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

func TestAvailableTVWithoutItemsCompletesDirectly(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	// Backfill case: an old tv request arrives straight as 'available'
	// (media long since downloaded, no items ever tracked).
	emit(t, s, "seerr", "available", SeerrOp{
		SeerrRequestID: 11, Kind: "available", MediaType: "tv", TvdbID: 999, Title: "Old Show",
	})
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 11)
	if req.Status != "completed" {
		t.Fatalf("zero-item available tv request must complete, got %s", req.Status)
	}
	// The rollup must not flip it back to active (0 items).
	st, err := s.RecomputeRequestStatus(ctx, req.ID)
	if err != nil || st != "completed" {
		t.Fatalf("recompute overrode explicit status: %s err=%v", st, err)
	}
	// And the fan-out sweep must ignore it.
	pending, _ := s.ActiveTVRequestsWithoutItems(ctx)
	if len(pending) != 0 {
		t.Fatalf("completed request leaked into fanout sweep: %+v", pending)
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

func TestTranscodeStage(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	movie := &MovieRef{RadarrID: 5, TmdbID: 693134, Title: "Dune"}
	emit(t, s, "seerr", "approved", SeerrOp{SeerrRequestID: 42, Kind: "approved", MediaType: "movie", TmdbID: 693134, Title: "Dune"})
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "dtr", Movie: movie})
	emit(t, s, "radarr", "import", ImportOp{Arr: "radarr", DownloadID: "dtr", Movie: movie, MoviePath: "/media/movies/Dune (2024)/dune.mkv"})
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 42)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	if items[0].CurrentStage != "imported" {
		t.Fatalf("setup: want imported, got %s", items[0].CurrentStage)
	}

	// Tdarr transcode matched by imported path (exact) → transcode stage.
	emit(t, s, "tdarr", "transcode", TranscodeOp{Phase: "start", File: "/media/movies/Dune (2024)/dune.mkv"})
	p.drain(ctx)
	if got, _ := s.GetMediaItem(ctx, items[0].ID); got.CurrentStage != "transcode" {
		t.Fatalf("want transcode after tdarr event, got %s", got.CurrentStage)
	}

	// Basename match works across a different mount prefix.
	emit(t, s, "tdarr", "transcode", TranscodeOp{Phase: "complete", File: "/other/root/dune.mkv"})
	p.drain(ctx)

	// Jellyfin available advances past the optional transcode waypoint.
	emit(t, s, "jellyfin", "available", AvailableOp{MediaItemID: items[0].ID, JellyfinItemID: "jf"})
	p.drain(ctx)
	got, _ := s.GetMediaItem(ctx, items[0].ID)
	if got.CurrentStage != "available" {
		t.Fatalf("want available, got %s", got.CurrentStage)
	}
	// The transcode transition stays in the item history.
	ts, _ := s.TransitionsForItem(ctx, items[0].ID)
	var hasTranscode bool
	for _, tr := range ts {
		if tr.Stage == "transcode" {
			hasTranscode = true
		}
	}
	if !hasTranscode {
		t.Fatal("transcode transition missing from history")
	}
}

func TestTranscodeIsOptionalWaypoint(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	movie := &MovieRef{RadarrID: 6, TmdbID: 700, Title: "X"}
	emit(t, s, "seerr", "approved", SeerrOp{SeerrRequestID: 43, Kind: "approved", MediaType: "movie", TmdbID: 700, Title: "X"})
	emit(t, s, "radarr", "grab", GrabOp{Arr: "radarr", DownloadID: "dx", Movie: movie})
	emit(t, s, "radarr", "import", ImportOp{Arr: "radarr", DownloadID: "dx", Movie: movie, MoviePath: "/m/x.mkv"})
	p.drain(ctx)

	req, _ := s.FindRequestBySeerrID(ctx, 43)
	items, _ := s.ListItemsForRequest(ctx, req.ID)
	// No transcode event ever fires → 'available' still advances (imported 70 → available 90).
	emit(t, s, "jellyfin", "available", AvailableOp{MediaItemID: items[0].ID})
	p.drain(ctx)
	if got, _ := s.GetMediaItem(ctx, items[0].ID); got.CurrentStage != "available" {
		t.Fatalf("without transcode, want available, got %s", got.CurrentStage)
	}
}
