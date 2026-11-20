package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

var (
	macToken = strings.Repeat("m", 40)
	haToken  = strings.Repeat("h", 40)
)

// fakeStack plays Seerr and Radarr on one server and records writes.
type fakeStack struct {
	mu     sync.Mutex
	writes []string
	bodies []string
}

func (f *fakeStack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodGet {
		f.mu.Lock()
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
	}
	switch {
	case r.URL.Path == "/api/v1/search":
		if r.URL.RawQuery != "query=the%20matrix&page=1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"totalPages":1,"results":[
			{"id":603,"mediaType":"movie","title":"The Matrix","releaseDate":"1999-03-31","overview":"Neo.","mediaInfo":{"status":5}},
			{"id":6384,"mediaType":"person","name":"Keanu Reeves"},
			{"id":1,"mediaType":"tv","name":"Matrix Show","firstAirDate":"2001-01-01"}]}`)
	case r.URL.Path == "/api/v1/request" && r.Method == http.MethodPost:
		_, _ = io.WriteString(w, `{"id":91,"status":2,"media":{"id":12,"status":3}}`)
	case r.URL.Path == "/api/v3/movie" && r.URL.Query().Get("tmdbId") == "603":
		_, _ = io.WriteString(w, `[{"id":7,"title":"The Matrix","tmdbId":603}]`)
	case r.URL.Path == "/api/v3/movie/7" && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `{"id":7,"title":"The Matrix","tmdbId":603,"monitored":true,"qualityProfileId":4,
			"minimumAvailability":"released","hasFile":true,"sizeOnDisk":2147483648,"extraField":"kept"}`)
	case r.URL.Path == "/api/v3/movie/7" && r.Method == http.MethodPut:
		_, _ = io.WriteString(w, `{}`)
	case r.URL.Path == "/api/v3/qualityprofile":
		_, _ = io.WriteString(w, `[{"id":4,"name":"HD-1080p"},{"id":6,"name":"Ultra-HD"}]`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newServer(t *testing.T, writeLimit int) (*Server, *store.Store, *fakeStack, string) {
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
	f := &fakeStack{}
	up := httptest.NewServer(f)
	t.Cleanup(up.Close)
	hc := &http.Client{Timeout: 5 * time.Second}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acts := &actions.Actions{Store: st, Log: log,
		Radarr: &clients.Arr{Name: "radarr", Kind: "radarr", BaseURL: up.URL, APIBase: "/api/v3", HTTP: hc},
		Seerr:  &clients.Seerr{BaseURL: up.URL, HTTP: hc},
	}
	s := New(Config{
		Tokens:  map[string]string{"mac": macToken, "ha": haToken},
		Writers: []string{"mac"}, Remove: "off", WriteLimit: writeLimit, Version: "test",
	}, Deps{Store: st, Actions: acts, Log: log})
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, st, f, srv.URL
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearer{token}, Timeout: 10 * time.Second},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	if res.IsError && len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			out = map[string]any{"error": tc.Text}
		}
	}
	return out, res.IsError
}

func TestRejectsMissingAndWrongToken(t *testing.T) {
	_, _, _, url := newServer(t, 30)
	for _, auth := range []string{"", "Bearer wrong-token-wrong-token-wrong-token"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth %q: status %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestToolsPerScope(t *testing.T) {
	s, _, _, url := newServer(t, 30)
	read := toolNames(t, connect(t, url, haToken))
	all := toolNames(t, connect(t, url, macToken))
	if !slices.Contains(read, "get_overview") || slices.Contains(read, "request_title") {
		t.Fatalf("read client sees %v", read)
	}
	if !slices.Contains(all, "request_title") || !slices.Contains(all, "update_title") {
		t.Fatalf("write client sees %v", all)
	}
	for _, name := range all {
		if strings.Contains(name, "remov") {
			t.Fatalf("removal tool %q present with JOURNARR_MCP_REMOVE=off", name)
		}
	}
	if len(s.Tools(false)) != len(read) || len(s.Tools(true)) != len(all) {
		t.Fatalf("Tools() disagrees with tools/list: %v / %v", s.Tools(false), s.Tools(true))
	}

	// A read client cannot call a write tool, not even by name.
	res, err := connect(t, url, haToken).CallTool(context.Background(),
		&mcp.CallToolParams{Name: "request_title", Arguments: map[string]any{"media_type": "movie", "tmdb_id": 603}})
	if err == nil && !res.IsError {
		t.Fatal("read client could call request_title")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchAndOverview(t *testing.T) {
	_, _, _, url := newServer(t, 30)
	cs := connect(t, url, haToken)

	out, isErr := call(t, cs, "search_titles", map[string]any{"query": "the matrix"})
	if isErr {
		t.Fatalf("search: %v", out)
	}
	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("want movie + series (person dropped), got %v", out)
	}
	first := results[0].(map[string]any)
	if first["status"] != "available" || first["tmdb_id"].(float64) != 603 || first["year"] != "1999" {
		t.Fatalf("first hit = %v", first)
	}

	out, isErr = call(t, cs, "get_overview", map[string]any{})
	if isErr || out["services"] == nil {
		t.Fatalf("overview = %v", out)
	}
}

func TestUpdateTitleEditsRadarrAndAudits(t *testing.T) {
	_, st, f, url := newServer(t, 30)
	cs := connect(t, url, macToken)

	out, isErr := call(t, cs, "update_title", map[string]any{
		"media_type": "movie", "tmdb_id": 603, "monitored": false, "quality_profile_id": 6})
	if isErr {
		t.Fatalf("update_title: %v", out)
	}
	if len(f.writes) != 1 || f.writes[0] != "PUT /api/v3/movie/7" {
		t.Fatalf("writes = %v", f.writes)
	}
	var put map[string]any
	_ = json.Unmarshal([]byte(f.bodies[0]), &put)
	if put["monitored"] != false || put["qualityProfileId"].(float64) != 6 || put["extraField"] != "kept" {
		t.Fatalf("PUT body = %s", f.bodies[0])
	}

	// An unknown profile is refused before anything is written.
	out, isErr = call(t, cs, "update_title", map[string]any{"media_type": "movie", "tmdb_id": 603, "quality_profile_id": 99})
	if !isErr || !strings.Contains(out["error"].(string), "does not exist") {
		t.Fatalf("unknown profile: %v", out)
	}

	list, _ := st.RecentActions(context.Background(), 5)
	if len(list) < 2 || list[1].Kind != "update_title" || list[1].Actor != "mcp:mac" || list[1].Status != "ok" {
		t.Fatalf("audit = %+v", list)
	}
}

func TestRequestTitleGoesThroughSeerr(t *testing.T) {
	_, _, f, url := newServer(t, 30)
	cs := connect(t, url, macToken)
	out, isErr := call(t, cs, "request_title", map[string]any{"media_type": "movie", "tmdb_id": 603})
	if isErr || out["seerr_request_id"].(float64) != 91 || out["request_status"] != "approved" {
		t.Fatalf("request_title = %v", out)
	}
	if f.writes[0] != "POST /api/v1/request" || !strings.Contains(f.bodies[0], `"mediaId":603`) {
		t.Fatalf("seerr call = %v %v", f.writes, f.bodies)
	}
	// a series without seasons is refused
	if out, isErr := call(t, cs, "request_title", map[string]any{"media_type": "tv", "tmdb_id": 1}); !isErr {
		t.Fatalf("series without seasons accepted: %v", out)
	}
}

func TestWriteLimit(t *testing.T) {
	_, _, _, url := newServer(t, 1)
	cs := connect(t, url, macToken)
	if out, isErr := call(t, cs, "request_title", map[string]any{"media_type": "movie", "tmdb_id": 603}); isErr {
		t.Fatalf("first write: %v", out)
	}
	out, isErr := call(t, cs, "request_title", map[string]any{"media_type": "movie", "tmdb_id": 603})
	if !isErr || !strings.Contains(out["error"].(string), "write limit") {
		t.Fatalf("second write: %v", out)
	}
	// reads are not limited
	if out, isErr := call(t, cs, "search_titles", map[string]any{"query": "the matrix"}); isErr {
		t.Fatalf("read after limit: %v", out)
	}
}

func TestHealthReason(t *testing.T) {
	cases := map[string]string{
		`{"error":"dial tcp: refused"}`:                   "dial tcp: refused",
		`{"health_messages":["Indexer down","Disk low"]}`: "Indexer down; Disk low",
		`{"restart_required":true}`:                       "Seerr asks for a restart",
		`{"poller":"TorBox poller stalled for 20m"}`:      "TorBox poller stalled for 20m",
		``: "",
	}
	for in, want := range cases {
		if got := healthReason(in); got != want {
			t.Errorf("healthReason(%s) = %q, want %q", in, got, want)
		}
	}
}

// fakeApprovals records removal requests.
type fakeApprovals struct {
	requested []actions.Title
	by        []string
}

func (f *fakeApprovals) Request(_ context.Context, t actions.Title, reason, by string) (*store.Approval, bool, error) {
	f.requested = append(f.requested, t)
	f.by = append(f.by, by)
	return &store.Approval{ID: 5, Title: "The Matrix", Status: "pending", RequestedBy: by, Reason: reason,
		Plan: `{"arr":"radarr","files":1,"size_bytes":2147483648}`}, false, nil
}

func (f *fakeApprovals) Get(_ context.Context, id int64) (*store.Approval, error) {
	return &store.Approval{ID: id, Title: "The Matrix", Status: "executed", DecidedBy: "owner@example.com",
		Result: `{"steps":[{"step":"radarr","status":"ok"}]}`}, nil
}

func (f *fakeApprovals) CountPending(context.Context) (int, error) { return len(f.requested), nil }

func TestRemovalToolsOnlyRequest(t *testing.T) {
	_, st, _, _ := newServer(t, 30)
	fa := &fakeApprovals{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Config{Tokens: map[string]string{"mac": macToken, "ha": haToken}, Writers: []string{"mac"},
		Remove: "approval", WriteLimit: 30, PublicURL: "https://status.example.com"},
		Deps{Store: st, Actions: &actions.Actions{Store: st, Log: log}, Approvals: fa, Log: log})
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	write := toolNames(t, connect(t, srv.URL, macToken))
	for _, want := range []string{"plan_removal", "request_removal", "get_removal_status"} {
		if !slices.Contains(write, want) {
			t.Fatalf("write client misses %s: %v", want, write)
		}
	}
	for _, name := range write {
		if strings.Contains(name, "approve") || name == "remove_title" {
			t.Fatalf("a tool that executes removals exists: %s", name)
		}
	}
	if read := toolNames(t, connect(t, srv.URL, haToken)); slices.Contains(read, "request_removal") {
		t.Fatalf("read client sees request_removal: %v", read)
	}

	cs := connect(t, srv.URL, macToken)
	out, isErr := call(t, cs, "request_removal", map[string]any{"media_type": "movie", "tmdb_id": 603, "reason": "watched"})
	if isErr || out["approve_url"] != "https://status.example.com/approvals?id=5" || out["status"] != "pending" {
		t.Fatalf("request_removal = %v", out)
	}
	if len(fa.by) != 1 || fa.by[0] != "mcp:mac" || fa.requested[0].TmdbID != 603 {
		t.Fatalf("approvals got %v by %v", fa.requested, fa.by)
	}
	// by Journarr request id alone, without media_type
	if out, isErr := call(t, cs, "request_removal", map[string]any{"request_id": 1, "reason": "watched"}); isErr {
		t.Fatalf("request_removal by request_id: %v", out)
	}
	if fa.requested[1].RequestID != 1 {
		t.Fatalf("request id not passed: %+v", fa.requested[1])
	}
	out, _ = call(t, cs, "get_removal_status", map[string]any{"approval_id": 5})
	if out["status"] != "executed" || out["decided_by"] != "owner@example.com" {
		t.Fatalf("status = %v", out)
	}
	out, _ = call(t, cs, "get_overview", map[string]any{})
	if out["pending_removal_requests"].(float64) != 2 {
		t.Fatalf("overview pending = %v", out["pending_removal_requests"])
	}
}
