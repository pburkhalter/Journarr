package store

import (
	"context"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyStageIdempotentAndForwardOnly(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	reqID, err := s.InsertOrphanRequest(ctx, "tv", "Test Show", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	season, episode := int64(1), int64(1)
	itemID, err := s.EnsureMediaItem(ctx, MediaItem{
		RequestID: &reqID, MediaType: "episode",
		SeasonNumber: &season, EpisodeNumber: &episode, Title: "Pilot",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First application lands.
	applied, err := s.ApplyStage(ctx, itemID, 1, "grabbed", 0, "")
	if err != nil || !applied {
		t.Fatalf("first apply: applied=%v err=%v", applied, err)
	}
	// Duplicate (webhook + poller) is a no-op.
	applied, err = s.ApplyStage(ctx, itemID, 1, "grabbed", 0, "")
	if err != nil || applied {
		t.Fatalf("duplicate apply: applied=%v err=%v", applied, err)
	}

	// Advance to imported, then a late-arriving 'grabbed' must not move
	// current_stage backwards.
	if _, err := s.ApplyStage(ctx, itemID, 1, "imported", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyStage(ctx, itemID, 1, "requested", 0, "late"); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMediaItem(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if m.CurrentStage != "imported" || m.CurrentCycle != 1 {
		t.Fatalf("want imported/1, got %s/%d", m.CurrentStage, m.CurrentCycle)
	}

	// New cycle: even an earlier stage wins because the cycle is higher.
	cycle, err := s.BumpItemCycle(ctx, itemID)
	if err != nil || cycle != 2 {
		t.Fatalf("bump: cycle=%d err=%v", cycle, err)
	}
	if _, err := s.ApplyStage(ctx, itemID, 2, "grabbed", 0, ""); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMediaItem(ctx, itemID)
	if m.CurrentStage != "grabbed" || m.CurrentCycle != 2 {
		t.Fatalf("want grabbed/2, got %s/%d", m.CurrentStage, m.CurrentCycle)
	}

	// History keeps both cycles.
	ts, err := s.TransitionsForItem(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 4 { // c1: grabbed, imported, requested(late) ; c2: grabbed
		t.Fatalf("want 4 transitions, got %d", len(ts))
	}
}

func TestRequestRollupStatus(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	seerrID := int64(42)
	reqID, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerrID, MediaType: "movie", Title: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := s.EnsureMediaItem(ctx, MediaItem{RequestID: &reqID, MediaType: "movie", Title: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.RecomputeRequestStatus(ctx, reqID)
	if err != nil || st != "active" {
		t.Fatalf("want active, got %s err=%v", st, err)
	}
	if _, err := s.ApplyStage(ctx, itemID, 1, "available", 0, ""); err != nil {
		t.Fatal(err)
	}
	st, err = s.RecomputeRequestStatus(ctx, reqID)
	if err != nil || st != "completed" {
		t.Fatalf("want completed, got %s err=%v", st, err)
	}

	// Upsert with same seerr id must not duplicate.
	again, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerrID, MediaType: "movie", Title: "Film Neu"})
	if err != nil || again != reqID {
		t.Fatalf("dedupe failed: %d vs %d err=%v", again, reqID, err)
	}
}

func TestDownloadUpsertAndInfohashReuse(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	id1, err := s.UpsertDownload(ctx, Download{ClientDownloadID: "ABCDEF123", Arr: "sonarr", ReleaseTitle: "Rel.1"})
	if err != nil {
		t.Fatal(err)
	}
	// Same id while active → same row (case-insensitive).
	id2, err := s.UpsertDownload(ctx, Download{ClientDownloadID: "abcdef123", Arr: "sonarr"})
	if err != nil || id2 != id1 {
		t.Fatalf("want same row %d, got %d err=%v", id1, id2, err)
	}
	// Terminal → a re-grab of the same infohash gets a fresh row.
	if err := s.SetDownloadState(ctx, id1, "failed", "boom"); err != nil {
		t.Fatal(err)
	}
	id3, err := s.UpsertDownload(ctx, Download{ClientDownloadID: "ABCDEF123", Arr: "sonarr"})
	if err != nil || id3 == id1 {
		t.Fatalf("want new row, got %d err=%v", id3, err)
	}
}
