package store

import (
	"context"
	"testing"
)

// A take-back clears the library stages of every attempt without an import
// (an earlier false notice must not suppress the real one) and keeps the
// attempts that were imported.
func TestRewindUnconfirmedAcrossAttempts(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	next := int64(0)
	item := func() int64 {
		next++
		tmdb, seerr := next, next
		reqID, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerr, MediaType: "movie", TmdbID: &tmdb, Title: "M"})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.EnsureMediaItem(ctx, MediaItem{RequestID: &reqID, MediaType: "movie", TmdbID: &tmdb, Title: "M"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	apply := func(id int64, cycle int, stages ...string) {
		for _, st := range stages {
			if _, err := s.ApplyStage(ctx, id, cycle, st, 0, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	bump := func(id int64) {
		if _, err := s.BumpItemCycle(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	stages := func(id int64) map[int][]string {
		trs, err := s.TransitionsForItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out := map[int][]string{}
		for _, tr := range trs {
			out[tr.Cycle] = append(out[tr.Cycle], tr.Stage)
		}
		return out
	}

	// Two false attempts: both lose their library stages.
	twice := item()
	apply(twice, 1, "requested", "available", "notified")
	bump(twice)
	apply(twice, 2, "grabbed", "available", "notified")
	stage, removed, err := s.RewindUnconfirmed(ctx, twice)
	if err != nil {
		t.Fatal(err)
	}
	if stage != "grabbed" || removed != 4 {
		t.Fatalf("rewind = %q, %d removed; want grabbed, 4", stage, removed)
	}
	got := stages(twice)
	if len(got[1]) != 1 || got[1][0] != "requested" || len(got[2]) != 1 || got[2][0] != "grabbed" {
		t.Fatalf("transitions after rewind = %v", got)
	}

	// An imported attempt keeps its notice; the later false one goes.
	upgraded := item()
	apply(upgraded, 1, "grabbed", "imported", "available", "notified")
	bump(upgraded)
	apply(upgraded, 2, "available")
	stage, removed, err = s.RewindUnconfirmed(ctx, upgraded)
	if err != nil {
		t.Fatal(err)
	}
	if stage != "requested" || removed != 1 {
		t.Fatalf("rewind = %q, %d removed; want requested, 1", stage, removed)
	}
	if got := stages(upgraded); len(got[1]) != 4 || len(got[2]) != 0 {
		t.Fatalf("transitions after rewind = %v", got)
	}

	// Only items past import without an import in their current attempt are
	// unconfirmed, and only their requests are listed.
	plain := item()
	apply(plain, 1, "grabbed", "imported", "available")
	claimed := item()
	apply(claimed, 1, "available")
	reqs, err := s.UnconfirmedRequests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claimedItem, _ := s.GetMediaItem(ctx, claimed)
	if len(reqs) != 1 || reqs[0].ID != *claimedItem.RequestID {
		t.Fatalf("unconfirmed requests = %+v, want only the request of item %d", reqs, claimed)
	}
}
