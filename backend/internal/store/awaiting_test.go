package store

import (
	"context"
	"testing"
	"time"
)

// TestRollupAwaitingReleaseDate guards the "Jan 1" bug: the awaiting date is
// read through a COALESCE/MIN expression, and modernc.org/sqlite returns those
// as raw strings — scanning straight into sql.NullTime silently yielded a zero
// time (0001-01-01). Both the TV request-level stamp and the movie item-level
// MIN path must round-trip to the real date.
func TestRollupAwaitingReleaseDate(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	want := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	assert := func(reqID int64, label string) {
		t.Helper()
		roll, err := s.RollupForRequest(ctx, reqID)
		if err != nil {
			t.Fatalf("%s: rollup: %v", label, err)
		}
		if roll.AwaitingReleaseAt == nil {
			t.Fatalf("%s: AwaitingReleaseAt is nil, want %s", label, want.Format("2006-01-02"))
		}
		if got := *roll.AwaitingReleaseAt; !got.Equal(want) {
			t.Errorf("%s: AwaitingReleaseAt = %s, want %s (0001-01-01 = the zero-time bug)",
				label, got.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	}

	// (a) TV: request-level stamp (COALESCE first arg).
	tvSeerr := int64(9001)
	tvID, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &tvSeerr, MediaType: "tv", Title: "Waiting Series"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestAwaiting(ctx, tvID, want); err != nil {
		t.Fatal(err)
	}
	assert(tvID, "tv request-level")

	// (b) Movie: item-level stamp (COALESCE falls through to MIN over items).
	mvSeerr := int64(9002)
	mvID, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &mvSeerr, MediaType: "movie", Title: "Waiting Movie"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := s.EnsureMediaItem(ctx, MediaItem{RequestID: &mvID, MediaType: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAwaitingRelease(ctx, itemID, want); err != nil {
		t.Fatal(err)
	}
	assert(mvID, "movie item-level")
}

// TestStaleAwaitingIgnoredOnceGrabbed guards the "done movie stuck in Waiting"
// bug: the release poller stops re-checking an item once it leaves
// requested/approved, so its awaiting stamp can go stale. A grabbed/available
// item must NOT keep the request pinned in Waiting.
func TestStaleAwaitingIgnoredOnceGrabbed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	seerr := int64(9101)
	rid, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerr, MediaType: "movie", Title: "Grabbed Movie"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := s.EnsureMediaItem(ctx, MediaItem{RequestID: &rid, MediaType: "movie", CurrentStage: "available"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAwaitingRelease(ctx, itemID, time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestStatus(ctx, rid, "completed"); err != nil {
		t.Fatal(err)
	}

	roll, err := s.RollupForRequest(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if roll.AwaitingReleaseAt != nil {
		t.Errorf("stale awaiting stamp on a grabbed item still surfaced: %v", roll.AwaitingReleaseAt)
	}
	waiting, _ := s.ListRequests(ctx, "waiting", "", 100, 0)
	for _, r := range waiting {
		if r.ID == rid {
			t.Error("grabbed movie wrongly listed under Waiting")
		}
	}
	done, _ := s.ListRequests(ctx, "done", "", 100, 0)
	found := false
	for _, r := range done {
		if r.ID == rid {
			found = true
		}
	}
	if !found {
		t.Error("completed movie not listed under Done")
	}
}

// TestFindRequestMatchesCompleted guards the duplicate-orphan bug: an upgrade /
// re-grab arriving after a request completed must re-attach to it, so the
// correlation lookups must match completed requests (the active-only variants
// did not, spawning a fresh orphan each time).
func TestFindRequestMatchesCompleted(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	seerr, tmdb := int64(9200), int64(555)
	rid, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerr, MediaType: "movie", Title: "Done Movie", TmdbID: &tmdb})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestStatus(ctx, rid, "completed"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FindRequestByTmdb(ctx, tmdb, "movie"); err != nil || got == nil || got.ID != rid {
		t.Fatalf("FindRequestByTmdb didn't match completed request: got=%v err=%v", got, err)
	}
	if a, _ := s.FindActiveRequestByTmdb(ctx, tmdb, "movie"); a != nil {
		t.Error("active-only lookup should NOT match a completed request (that's the bug it replaces)")
	}

	seerr2, tvdb := int64(9201), int64(777)
	rid2, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerr2, MediaType: "tv", Title: "Done Series", TvdbID: &tvdb})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestStatus(ctx, rid2, "completed"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FindRequestByTvdb(ctx, tvdb); err != nil || got == nil || got.ID != rid2 {
		t.Fatalf("FindRequestByTvdb didn't match completed tv request: got=%v err=%v", got, err)
	}
}

func TestParseSQLiteTime(t *testing.T) {
	cases := map[string]bool{
		"2026-07-13 03:00:00":           true,
		"2026-07-13 03:00:00.123456789": true,
		"2026-07-13T03:00:00Z":          true,
		"":                              false,
		"not-a-date":                    false,
	}
	for in, wantOK := range cases {
		if _, ok := parseSQLiteTime(in); ok != wantOK {
			t.Errorf("parseSQLiteTime(%q) ok=%v, want %v", in, ok, wantOK)
		}
	}
}
