package flow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/clients"
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

type fakeNotifier struct {
	down  bool
	calls int
	keys  []string
}

func (f *fakeNotifier) SendNotification(_ context.Context, n clients.Notification) (string, error) {
	f.calls++
	f.keys = append(f.keys, n.IdempotencyKey)
	if f.down {
		return "", errors.New("waha down")
	}
	return "msg-1", nil
}

func i64(v int64) *int64 { return &v }

// completedMovie creates a movie request whose item reached 'available'.
func completedMovie(t *testing.T, st *store.Store, tmdb int64) int64 {
	t.Helper()
	ctx := context.Background()
	seerrID := tmdb + 100
	reqID, err := st.UpsertRequest(ctx, store.Request{SeerrRequestID: &seerrID, MediaType: "movie", TmdbID: i64(tmdb), Title: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := st.EnsureMediaItem(ctx, store.MediaItem{RequestID: &reqID, MediaType: "movie", TmdbID: i64(tmdb), Title: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStage(ctx, itemID, 1, "available", 0, ""); err != nil {
		t.Fatal(err)
	}
	return reqID
}

// dueNow pulls every pending task's run_after into the past so drain picks it
// up without waiting out the backoff.
func dueNow(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE flow_tasks SET run_after = '2000-01-01 00:00:00' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
}

func taskStatuses(t *testing.T, st *store.Store, reqID int64) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT status FROM flow_tasks WHERE kind='notify' AND target_id=? ORDER BY id`, reqID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func newController(t *testing.T, st *store.Store, n *fakeNotifier) *Controller {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := New(st, log, &actions.Actions{Store: st, Log: log}, time.Second)
	c.Notifier = n
	c.NotifierHealthID = "notifyarr"
	if err := st.SetFlowSetting(context.Background(), "notify_on_complete", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func setHealth(t *testing.T, st *store.Store, status, detail string) {
	t.Helper()
	if err := st.UpsertServiceHealth(context.Background(), store.ServiceHealth{
		Service: "notifyarr", Status: status, Detail: detail, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// During the Aug 2026 WAHA outage every notify task ran out of retries and was
// marked failed for good: 232 items were never announced. A task must instead
// park as dead and be revived once the notifier reports healthy.
func TestNotifyTaskDiesAndRevivesWhenNotifierIsBack(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	reqID := completedMovie(t, st, 7)
	n := &fakeNotifier{down: true}
	c := newController(t, st, n)

	if _, err := st.EnqueueFlowTask(ctx, "notify", "request", reqID, "", store.NotifyTaskKey(reqID), time.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAttempts; i++ {
		dueNow(t, st)
		c.drain(ctx)
	}
	if got := taskStatuses(t, st, reqID); len(got) != 1 || got[0] != statusDead {
		t.Fatalf("task statuses = %v, want [dead]", got)
	}

	// Notifier still down: nothing is revived.
	setHealth(t, st, "down", "")
	c.sweep(ctx)
	if got := taskStatuses(t, st, reqID); len(got) != 1 {
		t.Fatalf("revived while notifier down: %v", got)
	}

	// Notifier back: the dead task is re-enqueued and delivered once.
	n.down = false
	setHealth(t, st, "up", "")
	c.nextRevive = time.Time{}
	c.sweep(ctx)
	if got := taskStatuses(t, st, reqID); len(got) != 2 || got[0] != "revived" || got[1] != "pending" {
		t.Fatalf("task statuses after revive = %v, want [revived pending]", got)
	}
	dueNow(t, st)
	c.drain(ctx)
	if got := taskStatuses(t, st, reqID); got[1] != "done" {
		t.Fatalf("task statuses after delivery = %v, want done", got)
	}
	if n.calls != maxAttempts+1 {
		t.Fatalf("notifier called %d times, want %d", n.calls, maxAttempts+1)
	}
	events, err := st.FetchUnprocessed(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	notified := 0
	for _, ev := range events {
		if ev.Source == "journarr" && ev.Kind == "notified" {
			notified++
		}
	}
	if notified != 1 {
		t.Fatalf("notified events = %d, want 1", notified)
	}
}

// A degraded notifyarr (any issue on its status file) still delivers as long
// as the WhatsApp session works.
func TestReviveTreatsWorkingWahaAsUp(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	reqID := completedMovie(t, st, 8)
	c := newController(t, st, &fakeNotifier{})
	if _, err := st.EnqueueFlowTask(ctx, "notify", "request", reqID, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE flow_tasks SET status = ?`, statusDead); err != nil {
		t.Fatal(err)
	}
	setHealth(t, st, "degraded", `{"waha":"WORKING","issues":["Fast pool 81% used"]}`)
	c.sweep(ctx)
	if got := taskStatuses(t, st, reqID); len(got) != 2 || got[1] != "pending" {
		t.Fatalf("task statuses = %v, want a revived pending task", got)
	}
}

// Retries after a timeout must carry the same idempotency key so notifyarr can
// answer with the message it already sent instead of sending it again.
func TestNotifyCarriesStableIdempotencyKey(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	reqID := completedMovie(t, st, 9)
	n := &fakeNotifier{down: true}
	c := newController(t, st, n)
	if _, err := st.EnqueueFlowTask(ctx, "notify", "request", reqID, "", store.NotifyTaskKey(reqID), time.Now()); err != nil {
		t.Fatal(err)
	}
	dueNow(t, st)
	c.drain(ctx)
	n.down = false
	dueNow(t, st)
	c.drain(ctx)
	if len(n.keys) != 2 || n.keys[0] != n.keys[1] || !strings.HasPrefix(n.keys[0], store.NotifyTaskKey(reqID)+":") {
		t.Fatalf("idempotency keys = %v, want two identical keys under %s", n.keys, store.NotifyTaskKey(reqID))
	}
}
