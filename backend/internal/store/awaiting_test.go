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
