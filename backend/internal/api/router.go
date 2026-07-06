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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/ingest"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/updates"
)

type Deps struct {
	Store   *store.Store
	Broker  *Broker
	Auth    *auth.Auth
	Ingest  *ingest.Handler // nil = no webhook ingestion
	Actions *actions.Actions
	Updates *updates.Checker // nil = no update checks
	Log     *slog.Logger
	Version string
	Dist    fs.FS // built frontend; may be empty pre-build
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Unauthenticated: Docker healthcheck, the SSO flow, and the
	// token-guarded webhook receivers.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "version": d.Version})
	})
	d.Auth.Routes(r)
	if d.Ingest != nil {
		d.Ingest.Routes(r)
	}

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
				// Merge in the GitHub update status for the custom stack.
				type svc struct {
					store.ServiceHealth
					Update *updates.Info `json:"update,omitempty"`
				}
				out := make([]svc, 0, len(list))
				for _, h := range list {
					s := svc{ServiceHealth: h}
					if d.Updates != nil {
						if info, ok := d.Updates.Get(h.Service); ok {
							s.Update = &info
						}
					}
					out = append(out, s)
				}
				writeJSON(w, map[string]any{"services": out})
			})
			r.Get("/stats", func(w http.ResponseWriter, req *http.Request) {
				st, err := d.Store.FetchStats(req.Context())
				if err != nil {
					httpError(w, d.Log, "fetch stats", err)
					return
				}
				stuck, _ := d.Store.CountStuck(req.Context())
				writeJSON(w, map[string]any{"requests": st.Requests, "media_items": st.MediaItems, "stuck": stuck})
			})
			r.Get("/events/stream", d.Broker.ServeHTTP)

			r.Get("/requests", func(w http.ResponseWriter, req *http.Request) {
				q := req.URL.Query()
				limit := clampInt(q.Get("limit"), 50, 1, 200)
				page := clampInt(q.Get("page"), 1, 1, 10000)
				list, err := d.Store.ListRequests(req.Context(),
					q.Get("status"), q.Get("q"), limit, (page-1)*limit)
				if err != nil {
					httpError(w, d.Log, "list requests", err)
					return
				}
				writeJSON(w, map[string]any{"requests": list, "page": page, "limit": limit})
			})

			r.Get("/requests/{id}", func(w http.ResponseWriter, req *http.Request) {
				id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
				if err != nil {
					http.Error(w, "bad id", http.StatusBadRequest)
					return
				}
				request, err := d.Store.RollupForRequest(req.Context(), id)
				if err != nil {
					httpError(w, d.Log, "get request", err)
					return
				}
				if request == nil {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				items, err := d.Store.ListItemsForRequest(req.Context(), id)
				if err != nil {
					httpError(w, d.Log, "list items", err)
					return
				}
				type itemDetail struct {
					store.MediaItem
					Transitions []store.StageTransition `json:"transitions"`
				}
				details := make([]itemDetail, 0, len(items))
				for _, it := range items {
					ts, err := d.Store.TransitionsForItem(req.Context(), it.ID)
					if err != nil {
						httpError(w, d.Log, "list transitions", err)
						return
					}
					details = append(details, itemDetail{MediaItem: it, Transitions: ts})
				}
				downloads, err := d.Store.DownloadsForRequest(req.Context(), id)
				if err != nil {
					httpError(w, d.Log, "list downloads", err)
					return
				}
				writeJSON(w, map[string]any{
					"request": request, "items": details, "downloads": downloads,
				})
			})

			r.Post("/actions/jellyfin-scan", func(w http.ResponseWriter, req *http.Request) {
				actx, cancel := detach()
				defer cancel()
				if err := d.Actions.JellyfinScan(actx); err != nil {
					httpError(w, d.Log, "jellyfin scan", err)
					return
				}
				writeJSON(w, map[string]string{"status": "ok"})
			})
			r.Post("/actions/retry", func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					MediaItemID int64 `json:"media_item_id"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.MediaItemID == 0 {
					http.Error(w, "media_item_id required", http.StatusBadRequest)
					return
				}
				actx, cancel := detach()
				defer cancel()
				if err := d.Actions.Retry(actx, body.MediaItemID); err != nil {
					httpError(w, d.Log, "retry", err)
					return
				}
				writeJSON(w, map[string]string{"status": "ok"})
			})
			r.Post("/actions/cancel", func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					RequestID int64 `json:"request_id"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.RequestID == 0 {
					http.Error(w, "request_id required", http.StatusBadRequest)
					return
				}
				actx, cancel := detach()
				defer cancel()
				if err := d.Actions.Cancel(actx, body.RequestID); err != nil {
					httpError(w, d.Log, "cancel", err)
					return
				}
				writeJSON(w, map[string]string{"status": "ok"})
			})

			r.Get("/media/{id}/events", func(w http.ResponseWriter, req *http.Request) {
				id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
				if err != nil {
					http.Error(w, "bad id", http.StatusBadRequest)
					return
				}
				events, err := d.Store.EventsForMedia(req.Context(), id, 100)
				if err != nil {
					httpError(w, d.Log, "list events", err)
					return
				}
				type evOut struct {
					ID         int64           `json:"id"`
					Source     string          `json:"source"`
					Kind       string          `json:"kind"`
					Payload    json.RawMessage `json:"payload"`
					ReceivedAt string          `json:"received_at"`
				}
				out := make([]evOut, 0, len(events))
				for _, e := range events {
					out = append(out, evOut{
						ID: e.ID, Source: e.Source, Kind: e.Kind,
						Payload:    json.RawMessage(e.Payload),
						ReceivedAt: e.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z"),
					})
				}
				writeJSON(w, map[string]any{"events": out})
			})
		})
	})

	r.NotFound(d.Auth.RequireBrowser(spaHandler(d.Dist)))
	return r
}

// detach returns a context independent of the HTTP request so a destructive
// action (retry/cancel) runs to completion even if the client disconnects
// mid-request, avoiding partial state. Bounded so it can't run forever.
func detach() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 90*time.Second)
}

// clampInt parses s with a default, bounded to [min, max].
func clampInt(s string, def, min, max int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		n = def
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
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
