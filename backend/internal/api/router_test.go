package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/approval"
	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/store"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestRouter serves the API in open LAN mode (no SSO) over a fresh store
// with one movie request and no upstream services.
func newTestRouter(t *testing.T) (http.Handler, *store.Store, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tmdb := int64(603)
	reqID, err := st.UpsertRequest(ctx, store.Request{MediaType: "movie", TmdbID: &tmdb, Title: "The Matrix"})
	if err != nil {
		t.Fatal(err)
	}
	log := quietLog()
	h := NewRouter(Deps{
		Store:     st,
		Broker:    NewBroker(),
		Auth:      auth.New(auth.Config{}, log),
		Actions:   &actions.Actions{Store: st, Log: log},
		Log:       log,
		Version:   "test",
		Dist:      fstest.MapFS{},
		PublicURL: "https://status.example.com",
	})
	return h, st, reqID
}

func do(h http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var sameOrigin = map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}

func TestHealthz(t *testing.T) {
	h, _, _ := newTestRouter(t)
	rec := do(h, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":"test"`) {
		t.Fatalf("healthz = %d %s", rec.Code, rec.Body)
	}
}

func TestCSRFRejectsCrossSiteAndNonJSON(t *testing.T) {
	h, _, _ := newTestRouter(t)

	// A cross-site form post (text/plain needs no preflight) must not reach
	// the handler.
	rec := do(h, http.MethodPost, "/api/actions/jellyfin-scan", `{}`,
		map[string]string{"Content-Type": "text/plain"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d, want 415", rec.Code)
	}
	rec = do(h, http.MethodPost, "/api/actions/jellyfin-scan", `{}`,
		map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d, want 403", rec.Code)
	}
	rec = do(h, http.MethodPost, "/api/actions/jellyfin-scan", `{}`,
		map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d, want 403", rec.Code)
	}
	// Behind the proxy the public origin is trusted even if Host differs.
	rec = do(h, http.MethodPost, "/api/actions/jellyfin-scan", `{}`,
		map[string]string{"Content-Type": "application/json", "Origin": "https://status.example.com"})
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("public origin rejected: %d", rec.Code)
	}
	// Reads stay open to GET without a content type.
	if rec := do(h, http.MethodGet, "/api/stats", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET stats: %d", rec.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	h, _, _ := newTestRouter(t)
	big := `{"settings":{"notify_stage":"` + strings.Repeat("x", maxBodyBytes) + `"}}`
	rec := do(h, http.MethodPut, "/api/flow", big, sameOrigin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d, want 400", rec.Code)
	}
}

func TestRemoveAnswersPlanChanged(t *testing.T) {
	h, st, reqID := newTestRouter(t)

	rec := do(h, http.MethodGet, "/api/requests/"+itoa(reqID)+"/removal", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var plan struct {
		PlanHash string `json:"plan_hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil || plan.PlanHash == "" {
		t.Fatalf("preview has no plan_hash: %s", rec.Body)
	}

	rec = do(h, http.MethodPost, "/api/actions/remove",
		`{"request_id":`+itoa(reqID)+`,"plan_hash":"stale"}`, sameOrigin)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"plan_changed"`) {
		t.Fatalf("stale hash: %d %s", rec.Code, rec.Body)
	}
	if r, _ := st.GetRequest(context.Background(), reqID); r == nil {
		t.Fatal("request removed despite a stale plan")
	}

	rec = do(h, http.MethodPost, "/api/actions/remove",
		`{"request_id":`+itoa(reqID)+`,"plan_hash":"`+plan.PlanHash+`"}`, sameOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/requests/"+itoa(reqID)+"/removal", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("preview after removal: %d, want 404", rec.Code)
	}
}

func TestActionAuditRecordsActor(t *testing.T) {
	h, st, _ := newTestRouter(t)
	// Jellyfin is not configured, so the scan fails — but it is audited.
	_ = do(h, http.MethodPost, "/api/actions/jellyfin-scan", `{}`, sameOrigin)
	list, err := st.RecentActions(context.Background(), 5)
	if err != nil || len(list) == 0 {
		t.Fatalf("no audit row: %v", err)
	}
	if a := list[0]; a.Kind != "jellyfin_scan" || a.Actor != "anonymous" || a.Status != "failed" {
		t.Fatalf("audit row = %+v", a)
	}
	rec := do(h, http.MethodGet, "/api/actions/log", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"actor":"anonymous"`) {
		t.Fatalf("action log: %d %s", rec.Code, rec.Body)
	}
}

// In open LAN mode nobody may approve a removal: the request stays pending
// and nothing is deleted, whatever the caller sends.
func TestApprovalNeedsSSO(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tmdb := int64(603)
	reqID, _ := st.UpsertRequest(ctx, store.Request{MediaType: "movie", TmdbID: &tmdb, Title: "The Matrix"})
	log := quietLog()
	acts := &actions.Actions{Store: st, Log: log}
	appr := &approval.Service{Store: st, Actions: acts, Log: log}
	h := NewRouter(Deps{Store: st, Broker: NewBroker(), Auth: auth.New(auth.Config{}, log), Actions: acts,
		Log: log, Dist: fstest.MapFS{}, Approvals: appr})

	a, _, err := appr.Request(ctx, actions.Title{RequestID: reqID}, "watched", "mcp:mac")
	if err != nil {
		t.Fatal(err)
	}
	rec := do(h, http.MethodGet, "/api/approvals", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"requested_by":"mcp:mac"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/approvals/"+itoa(a.ID), "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"plan_changed":false`) {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	for _, hdr := range []map[string]string{
		sameOrigin,
		{"Content-Type": "application/json", "Authorization": "Bearer whatever-token-whatever-token-x"},
	} {
		rec = do(h, http.MethodPost, "/api/approvals/"+itoa(a.ID)+"/approve", `{}`, hdr)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("approve %v: %d %s", hdr, rec.Code, rec.Body)
		}
	}
	if got, _ := appr.Get(ctx, a.ID); got.Status != "pending" {
		t.Fatalf("status = %s, want pending", got.Status)
	}
	if r, _ := st.GetRequest(ctx, reqID); r == nil {
		t.Fatal("request was removed")
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
