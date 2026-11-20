// Package mcpserver exposes Journarr and the stack behind it to LLM clients
// over the Model Context Protocol (Streamable HTTP, stateless, JSON answers).
//
// Every client has its own bearer token (JOURNARR_MCP_TOKEN_<CLIENT>). A
// token grants "read", or "read" + "write" for the clients listed in
// JOURNARR_MCP_WRITE. A client only ever sees the tools of its scope: the
// handler builds one MCP server per scope and picks it per request.
//
// Removal is never executed from here. With JOURNARR_MCP_REMOVE=approval a
// write client may file a removal request; the owner approves it in the
// Journarr UI with an SSO session, which no MCP token can provide.
package mcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/audit"
	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/storage"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/updates"
)

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// Config is the MCP part of Journarr's configuration.
type Config struct {
	Tokens     map[string]string // client -> token
	Writers    []string          // clients with the write scope
	Remove     string            // off|approval
	WriteLimit int               // write calls per client per hour
	PublicURL  string            // for links into the UI
	Version    string
}

// Deps are the parts of Journarr the tools use.
type Deps struct {
	Store    *store.Store
	Actions  *actions.Actions
	Registry *registry.Registry
	Storage  *storage.Service
	Updates  *updates.Checker
	// Approvals is required for Remove == approval.
	Approvals Approvals
	Log       *slog.Logger
}

// Server is the /mcp handler.
type Server struct {
	cfg     Config
	d       Deps
	writers map[string]bool
	limiter *rateLimiter
	entries []entry
	byScope map[string]*mcp.Server
	handler http.Handler
}

type entry struct {
	name    string
	write   bool
	install func(*mcp.Server)
}

const instructions = `Journarr tracks every movie and series request through the home media stack:
Seerr (requests) → Sonarr (series) / Radarr (movies) → download → import → Jellyfin → WhatsApp notice.

- Find titles with search_titles; it returns TMDB ids and whether a title is available, requested or new.
- Add titles with request_title. It goes through Seerr, so Journarr tracks it and the household gets the
  usual notice when it is ready. Do not add titles any other way.
- list_requests / get_request show where a request is in the pipeline (stage per episode or movie, stuck items).
- list_library / get_title show what Sonarr/Radarr hold; update_title and search_title change or re-search it.
- Removing a title is never done directly: request_removal files a request that the owner approves in the
  Journarr UI. Tell the user it waits for their approval there; check it with get_removal_status.
- Prefer few, targeted calls; list tools are paged (limit/offset).`

// New builds the MCP handler. It returns nil when no client token is set.
func New(cfg Config, d Deps) *Server {
	if len(cfg.Tokens) == 0 {
		return nil
	}
	s := &Server{cfg: cfg, d: d, writers: map[string]bool{}, limiter: newRateLimiter(cfg.WriteLimit, time.Hour)}
	for _, w := range cfg.Writers {
		s.writers[w] = true
	}
	s.registerReadTools()
	s.registerWriteTools()
	if cfg.Remove == "approval" && d.Approvals != nil {
		s.registerRemovalTools()
	}

	s.byScope = map[string]*mcp.Server{
		ScopeRead:  s.build(false),
		ScopeWrite: s.build(true),
	}
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if ti := auth.TokenInfoFromContext(r.Context()); ti != nil && hasScope(ti.Scopes, ScopeWrite) {
			return s.byScope[ScopeWrite]
		}
		return s.byScope[ScopeRead]
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: nil})
	s.handler = auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{
		Scopes:                 []string{ScopeRead},
		AllowMissingExpiration: true, // static per-client tokens
	})(streamable)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Tools lists the tool names a scope sees (tests, startup log).
func (s *Server) Tools(write bool) []string {
	var out []string
	for _, e := range s.entries {
		if !e.write || write {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) build(write bool) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "journarr", Title: "Journarr", Version: s.cfg.Version},
		&mcp.ServerOptions{Instructions: instructions})
	for _, e := range s.entries {
		if !e.write || write {
			e.install(srv)
		}
	}
	return srv
}

// verify maps a bearer token to its client, in constant time per token.
func (s *Server) verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	var client string
	for name, want := range s.cfg.Tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1 {
			client = name
		}
	}
	if client == "" {
		return nil, auth.ErrInvalidToken
	}
	scopes := []string{ScopeRead}
	if s.writers[client] {
		scopes = append(scopes, ScopeWrite)
	}
	return &auth.TokenInfo{Scopes: scopes, UserID: client}, nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// addTool registers a typed tool. The wrapper names the client as audit
// actor, enforces the write budget, and logs every call.
func addTool[In any](s *Server, def *mcp.Tool, write bool, run func(ctx context.Context, in In) (any, error)) {
	h := func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		client := "unknown"
		if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil {
			client = req.Extra.TokenInfo.UserID
		}
		ctx = audit.WithActor(ctx, "mcp:"+client)
		if write && !s.limiter.allow(client) {
			s.d.Log.Warn("mcp: write budget exhausted", "client", client, "tool", def.Name)
			return nil, nil, fmt.Errorf("write limit reached (%d calls per hour for %s); try again later", s.cfg.WriteLimit, client)
		}
		start := time.Now()
		out, err := run(ctx, in)
		args, _ := json.Marshal(in)
		attrs := []any{"client", client, "tool", def.Name, "args", truncate(string(args), 300),
			"ms", time.Since(start).Milliseconds()}
		if err != nil {
			s.d.Log.Info("mcp: tool call failed", append(attrs, "err", err)...)
			return nil, nil, err
		}
		s.d.Log.Info("mcp: tool call", attrs...)
		return nil, out, nil
	}
	s.entries = append(s.entries, entry{name: def.Name, write: write, install: func(srv *mcp.Server) {
		mcp.AddTool(srv, def, h)
	}})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// rateLimiter allows n calls per client in a sliding window.
type rateLimiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	calls  map[string][]time.Time
	now    func() time.Time
}

func newRateLimiter(n int, window time.Duration) *rateLimiter {
	return &rateLimiter{n: n, window: window, calls: map[string][]time.Time{}, now: time.Now}
}

func (l *rateLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	keep := l.calls[client][:0]
	for _, t := range l.calls[client] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.n {
		l.calls[client] = keep
		return false
	}
	l.calls[client] = append(keep, now)
	return true
}

func ptr[T any](v T) *T { return &v }

// readOnly / mutating are the tool annotations of the two scopes.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func mutating(title string, idempotent, destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, IdempotentHint: idempotent,
		DestructiveHint: ptr(destructive), OpenWorldHint: ptr(false)}
}
