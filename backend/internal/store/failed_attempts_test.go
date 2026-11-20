package store

import (
	"context"
	"testing"
)

// Migration 0012 repairs items a failed download left at a pre-import stage
// and clears stale errors on items past import. Re-run on fixtures here.
func TestMigrationResetsFailedAttempts(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	next := int64(0)
	item := func(stage, lastErr string, dlState string) int64 {
		next++
		tmdb, seerr := next, next
		reqID, err := s.UpsertRequest(ctx, Request{SeerrRequestID: &seerr, MediaType: "movie", TmdbID: &tmdb, Title: stage})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.EnsureMediaItem(ctx, MediaItem{RequestID: &reqID, MediaType: "movie", TmdbID: &tmdb, Title: stage})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyStage(ctx, id, 1, stage, 0, ""); err != nil {
			t.Fatal(err)
		}
		if dlState != "" {
			dl, err := s.UpsertDownload(ctx, Download{Arr: "radarr", ClientDownloadID: "dl-" + stage, State: dlState})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.LinkDownloadItem(ctx, dl, id, 1); err != nil {
				t.Fatal(err)
			}
			if err := s.SetDownloadState(ctx, dl, dlState, ""); err != nil {
				t.Fatal(err)
			}
		}
		if lastErr != "" {
			if err := s.SetItemError(ctx, id, lastErr); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	stuck := item("downloaded", "Manually marked as failed", "failed")
	running := item("grabbed", "", "grabbed")
	done := item("available", "Manually marked as failed", "imported")

	body, err := migrationsFS.ReadFile("migrations/0012_failed_attempts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	get := func(id int64) *MediaItem {
		m, err := s.GetMediaItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := get(stuck); m.CurrentStage != "requested" || m.CurrentCycle != 2 || m.LastError != "" {
		t.Fatalf("failed attempt not reset: %s/%d %q", m.CurrentStage, m.CurrentCycle, m.LastError)
	}
	if m := get(running); m.CurrentStage != "grabbed" || m.CurrentCycle != 1 {
		t.Fatalf("running download touched: %s/%d", m.CurrentStage, m.CurrentCycle)
	}
	if m := get(done); m.CurrentStage != "available" || m.LastError != "" {
		t.Fatalf("finished item keeps a stale error: %s %q", m.CurrentStage, m.LastError)
	}
}
