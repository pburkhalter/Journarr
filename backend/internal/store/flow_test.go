package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFlowTaskDedupeAndFinishClearsKey(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	ins, err := s.EnqueueFlowTask(ctx, "jellyfin_scan", "", 0, "", "jellyfin_scan", time.Now())
	if err != nil || !ins {
		t.Fatalf("first enqueue should insert: ins=%v err=%v", ins, err)
	}
	// Duplicate while pending → coalesced.
	if ins2, _ := s.EnqueueFlowTask(ctx, "jellyfin_scan", "", 0, "", "jellyfin_scan", time.Now()); ins2 {
		t.Fatal("duplicate pending enqueue should be ignored")
	}

	tasks, _ := s.ClaimFlowTasks(ctx, 10)
	if len(tasks) != 1 {
		t.Fatalf("want 1 claimable, got %d", len(tasks))
	}
	if err := s.FinishFlowTask(ctx, tasks[0].ID, "done"); err != nil {
		t.Fatal(err)
	}
	// Finished → not claimable.
	if again, _ := s.ClaimFlowTasks(ctx, 10); len(again) != 0 {
		t.Fatalf("finished task still claimable: %d", len(again))
	}
	// Key cleared → the same logical task can recur.
	if ins3, _ := s.EnqueueFlowTask(ctx, "jellyfin_scan", "", 0, "", "jellyfin_scan", time.Now()); !ins3 {
		t.Fatal("after finish, same-key enqueue should insert again")
	}
}

func TestFlowTaskRunAfterGate(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.EnqueueFlowTask(ctx, "retry", "media_item", 5, "", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := s.ClaimFlowTasks(ctx, 10); len(tasks) != 0 {
		t.Fatalf("future task should not be claimed yet, got %d", len(tasks))
	}
}

func TestFlowTaskReschedule(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	s.EnqueueFlowTask(ctx, "retry", "media_item", 7, "", "retry:7:1", time.Now())
	tasks, _ := s.ClaimFlowTasks(ctx, 10)
	if len(tasks) != 1 {
		t.Fatalf("want 1, got %d", len(tasks))
	}
	// Reschedule into the future → no longer claimable, attempts bumped.
	if err := s.RescheduleFlowTask(ctx, tasks[0].ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.ClaimFlowTasks(ctx, 10); len(again) != 0 {
		t.Fatalf("rescheduled task should not be claimable, got %d", len(again))
	}
}

func TestFlowSettingsUpsert(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.SetFlowSetting(ctx, "notify_on_complete", "true"); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetFlowSettings(ctx)
	if m["notify_on_complete"] != "true" {
		t.Fatalf("setting not persisted: %v", m)
	}
	// Upsert to a new value.
	s.SetFlowSetting(ctx, "notify_on_complete", "false")
	m, _ = s.GetFlowSettings(ctx)
	if m["notify_on_complete"] != "false" {
		t.Fatalf("upsert failed: %v", m)
	}
}
