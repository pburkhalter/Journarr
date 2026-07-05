package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/store"
)

type Deps struct {
	Store   *store.Store
	Broker  *Broker
	Auth    *auth.Auth
	Log     *slog.Logger
	Version string
	Dist    fs.FS // built frontend; may be empty pre-build
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Unauthenticated: Docker healthcheck + the SSO flow itself.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "version": d.Version})
	})
	d.Auth.Routes(r)

	r.Route("/api", func(r chi.Router) {
		// Open: the SPA probes /api/me to render the user chip; it answers
		// 401 itself when a session is missing.
		r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
			if !d.Auth.Enabled() {
				writeJSON(w, map[string]any{"auth_enabled": false})
				return
			}
			u, ok := d.Auth.UserFrom(req)
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
				return
			}
			writeJSON(w, map[string]any{"auth_enabled": true, "user": u})
		})

		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireAPI)
			r.Get("/services", func(w http.ResponseWriter, req *http.Request) {
				list, err := d.Store.ListServiceHealth(req.Context())
				if err != nil {
					httpError(w, d.Log, "list services", err)
					return
				}
				writeJSON(w, map[string]any{"services": list})
			})
			r.Get("/stats", func(w http.ResponseWriter, req *http.Request) {
				st, err := d.Store.FetchStats(req.Context())
				if err != nil {
					httpError(w, d.Log, "fetch stats", err)
					return
				}
				writeJSON(w, st)
			})
			r.Get("/events/stream", d.Broker.ServeHTTP)
		})
	})

	r.NotFound(d.Auth.RequireBrowser(spaHandler(d.Dist)))
	return r
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, log *slog.Logger, msg string, err error) {
	log.Error(msg, "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
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
				http.ServeFileFS(w, r, dist, p)
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
