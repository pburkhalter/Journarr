package actions

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func hasKind(ds []Descriptor, kind string) bool {
	for _, d := range ds {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

// "Resend notification" is offered only while a request has items at the
// notify stage that were never announced, and it queues exactly one task.
func TestResendNotifyOfferedForUnannouncedItems(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	reg, err := registry.Build([]registry.Spec{{ID: "notifyarr", Kind: registry.KindNotifyarr, URL: "http://n"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := &Actions{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Reg: reg}

	seerrID, tmdb := int64(1), int64(5)
	reqID, err := st.UpsertRequest(ctx, store.Request{SeerrRequestID: &seerrID, MediaType: "movie", TmdbID: &tmdb, Title: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := st.EnsureMediaItem(ctx, store.MediaItem{RequestID: &reqID, MediaType: "movie", TmdbID: &tmdb})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStage(ctx, itemID, 1, "available", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !hasKind(a.Available(ctx, "request", reqID), "resend-notify") {
		t.Fatal("resend-notify not offered for an unannounced available item")
	}

	if err := a.Execute(ctx, "resend-notify", map[string]any{"request_id": float64(reqID)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Execute(ctx, "resend-notify", map[string]any{"request_id": float64(reqID)}); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ClaimFlowTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Kind != "notify" || tasks[0].TargetID != reqID {
		t.Fatalf("flow tasks = %+v, want one pending notify for request %d", tasks, reqID)
	}

	// Once announced, the action disappears.
	if _, err := st.ApplyStage(ctx, itemID, 1, "notified", 0, ""); err != nil {
		t.Fatal(err)
	}
	if hasKind(a.Available(ctx, "request", reqID), "resend-notify") {
		t.Fatal("resend-notify still offered after the item was notified")
	}
}
