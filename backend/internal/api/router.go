package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/approval"
	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/flow"
	"github.com/pburkhalter/journarr/internal/ingest"
	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/storage"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/updates"
)

type Deps struct {
	Store    *store.Store
	Broker   *Broker
	Auth     *auth.Auth
	Ingest   *ingest.Handler // nil = no webhook ingestion
	Actions  *actions.Actions
	Registry *registry.Registry
	Storage  *storage.Service // nil = disk-space endpoint returns empty
	Flow     *flow.Controller // nil = control-plane disabled
	Updates  *updates.Checker // nil = no update checks
	Log      *slog.Logger
	Version  string
	Dist     fs.FS // built frontend; may be empty pre-build
	// PublicURL is the external base URL; its origin is trusted for
	// state-changing browser requests behind a proxy that rewrites Host.
	PublicURL string
	// Tasks, when set, tracks detached actions so shutdown can wait for them
	// before the store closes.
	Tasks *sync.WaitGroup
	// MCP, when set, is mounted at /mcp outside the SSO gate; it brings its
	// own bearer-token authentication.
	MCP http.Handler
	// Approvals holds the removal requests of MCP clients; nil = feature off.
	Approvals *approval.Service
}

// server holds the dependencies the handlers share.
type server struct {
	Deps
}

// maxBodyBytes bounds JSON request bodies on /api.
const maxBodyBytes = 1 << 20

func NewRouter(d Deps) http.Handler {
	s := &server{Deps: d}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Unauthenticated: Docker healthcheck, the SSO flow, and the
	// token-guarded webhook receivers.
	r.Get("/healthz", s.healthz)
	d.Auth.Routes(r)
	if d.Ingest != nil {
		d.Ingest.Routes(r)
	}
	if d.MCP != nil {
		r.Handle("/mcp", d.MCP)
	}

	r.Route("/api", func(r chi.Router) {
		// Open: the SPA probes /api/me to render the user chip; it answers
		// 401 itself when a session is missing.
		r.Get("/me", s.me)

		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireAPI, s.withActor, limitBody(maxBodyBytes), s.csrf())
			s.serviceRoutes(r)
			s.flowRoutes(r)
			s.requestRoutes(r)
			s.actionRoutes(r)
			s.approvalRoutes(r)
			r.Get("/events/stream", d.Broker.ServeHTTP)
		})
	})

	r.NotFound(d.Auth.RequireBrowser(spaHandler(d.Dist)))
	return r
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok", "version": s.Version})
}

func (s *server) me(w http.ResponseWriter, req *http.Request) {
	if !s.Auth.Enabled() {
		writeJSON(w, map[string]any{"auth_enabled": false})
		return
	}
	u, ok := s.Auth.UserFrom(req)
	if !ok {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	writeJSON(w, map[string]any{"auth_enabled": true, "user": u})
}

// detach returns a context that outlives the HTTP request (keeping its
// values, e.g. the audit actor) so a destructive action runs to completion
// even if the client disconnects mid-request, avoiding partial state. Bounded
// so it can't run forever; tracked so shutdown can wait for it.
func (s *server) detach(req *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 90*time.Second)
	if s.Tasks == nil {
		return ctx, cancel
	}
	s.Tasks.Add(1)
	var once sync.Once
	return ctx, func() {
		cancel()
		once.Do(s.Tasks.Done)
	}
}

// decodeJSON reads a JSON request body into v.
func decodeJSON(req *http.Request, v any) error {
	return json.NewDecoder(req.Body).Decode(v)
}

// pathID parses the {id} URL parameter.
func pathID(req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	return id, err == nil && id > 0
}

// clampInt parses s with a default, bounded to [lo, hi].
func clampInt(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		n = def
	}
	return min(max(n, lo), hi)
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// httpError logs err and answers 500 with msg only — upstream error texts
// can carry URLs and are not for the browser.
func httpError(w http.ResponseWriter, log *slog.Logger, msg string, err error) {
	log.Error(msg, "err", err)
	writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

// spaHandler serves the embedded frontend: exact file matches first, then
// index.html for client-side routes, then a plain notice pre-frontend-build.
func spaHandler(dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := dist.Open(p); err == nil {
			if st, err := f.Stat(); err == nil && !st.IsDir() {
				_ = f.Close()
				http.ServeFileFS(w, r, dist, p) //nolint:gosec // p is path.Clean-ed and dist is the embedded fs.FS, which cannot reach outside
				return
			}
			_ = f.Close()
		}
		if _, err := fs.Stat(dist, "index.html"); err == nil {
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Journarr API is running — frontend not built.\n"))
	}
}
