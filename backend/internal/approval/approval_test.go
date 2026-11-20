package approval

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/store"
)

// fakeRemover plans a fixed title and records removals.
type fakeRemover struct {
	hash    string
	removed []string // plan hashes passed to Remove
	forced  []bool
	fail    error
}

func (f *fakeRemover) PlanRemoval(_ context.Context, t actions.Title) (*actions.RemovalPlan, error) {
	return &actions.RemovalPlan{Title: "The Matrix", MediaType: t.MediaType, TmdbID: t.TmdbID,
		Arr: "radarr", ArrID: 7, PlanHash: f.hash}, nil
}

func (f *fakeRemover) Remove(_ context.Context, _ actions.Title, planHash string, force bool) ([]actions.RemovalStep, error) {
	f.removed = append(f.removed, planHash)
	f.forced = append(f.forced, force)
	if f.fail != nil {
		return []actions.RemovalStep{{Step: "radarr", Status: "skipped"}}, f.fail
	}
	return []actions.RemovalStep{{Step: "radarr", Status: "ok"}}, nil
}

func newService(t *testing.T) (*Service, *fakeRemover, *time.Time) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	f := &fakeRemover{hash: "h1"}
	s := &Service{Store: st, Actions: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		TTL: time.Hour, Now: func() time.Time { return now }}
	return s, f, &now
}

var matrix = actions.Title{MediaType: "movie", TmdbID: 603}

func TestRequestNeedsReasonAndIsIdempotent(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	if _, _, err := s.Request(ctx, matrix, "  ", "mcp:mac"); err == nil {
		t.Fatal("empty reason accepted")
	}
	a, existing, err := s.Request(ctx, matrix, "watched, frees 20 GB", "mcp:mac")
	if err != nil || existing || a.Status != "pending" || a.PlanHash != "h1" || a.RequestedBy != "mcp:mac" {
		t.Fatalf("first request = %+v existing=%v err=%v", a, existing, err)
	}
	b, existing, err := s.Request(ctx, matrix, "again", "mcp:agentbox")
	if err != nil || !existing || b.ID != a.ID {
		t.Fatalf("second request = %+v existing=%v err=%v", b, existing, err)
	}
	if n, _ := s.CountPending(ctx); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
}

func TestApproveRunsTheReviewedPlanOnce(t *testing.T) {
	s, f, _ := newService(t)
	ctx := context.Background()
	a, _, _ := s.Request(ctx, matrix, "watched", "mcp:mac")

	done, err := s.Approve(ctx, a.ID, "owner@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "executed" || done.DecidedBy != "owner@example.com" || done.Result == "" {
		t.Fatalf("after approve = %+v", done)
	}
	if len(f.removed) != 1 || f.removed[0] != "h1" || f.forced[0] {
		t.Fatalf("Remove calls = %v forced=%v", f.removed, f.forced)
	}
	if _, err := s.Approve(ctx, a.ID, "owner@example.com", false); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve: %v, want ErrNotPending", err)
	}
	if len(f.removed) != 1 {
		t.Fatal("second approve removed again")
	}
}

func TestApproveRecordsAFailedRemoval(t *testing.T) {
	s, f, _ := newService(t)
	ctx := context.Background()
	f.fail = actions.ErrPlanChanged
	a, _, _ := s.Request(ctx, matrix, "watched", "mcp:mac")
	got, err := s.Approve(ctx, a.ID, "owner", true)
	if !errors.Is(err, actions.ErrPlanChanged) || got == nil || got.Status != "failed" {
		t.Fatalf("approve = %+v, %v", got, err)
	}
	if !f.forced[0] {
		t.Fatal("force not passed through")
	}
}

func TestRejectAndExpire(t *testing.T) {
	s, f, now := newService(t)
	ctx := context.Background()
	a, _, _ := s.Request(ctx, matrix, "watched", "mcp:mac")
	if got, err := s.Reject(ctx, a.ID, "owner"); err != nil || got.Status != "rejected" {
		t.Fatalf("reject = %+v, %v", got, err)
	}
	if _, err := s.Approve(ctx, a.ID, "owner", false); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve after reject: %v", err)
	}

	b, _, _ := s.Request(ctx, actions.Title{MediaType: "movie", TmdbID: 604}, "watched", "mcp:mac")
	*now = now.Add(2 * time.Hour) // past the 1 h TTL
	if _, err := s.Approve(ctx, b.ID, "owner", false); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve after deadline: %v", err)
	}
	s.ExpireDue(ctx)
	if got, _ := s.Get(ctx, b.ID); got.Status != "expired" {
		t.Fatalf("status after ExpireDue = %s", got.Status)
	}
	if len(f.removed) != 0 {
		t.Fatalf("something was removed: %v", f.removed)
	}
	if _, err := s.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
