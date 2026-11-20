package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/pburkhalter/journarr/internal/actions"
)

func (s *server) actionRoutes(r chi.Router) {
	r.Get("/actions", s.listActions)
	r.Get("/actions/log", s.actionLog)
	r.Post("/actions/execute", s.executeAction)
	r.Post("/actions/jellyfin-scan", s.jellyfinScan)
	r.Post("/actions/retry", s.retry)
	r.Post("/actions/cancel", s.cancel)
	r.Post("/actions/remove", s.remove)
}

// listActions is the capability-derived action catalog for the Actions tab
// and the detail view.
func (s *server) listActions(w http.ResponseWriter, req *http.Request) {
	scope := req.URL.Query().Get("scope")
	if scope == "" {
		scope = "global"
	}
	targetID, _ := strconv.ParseInt(req.URL.Query().Get("target_id"), 10, 64)
	writeJSON(w, map[string]any{"actions": s.Actions.Available(req.Context(), scope, targetID)})
}

// actionLog returns the newest audit rows (who did what).
func (s *server) actionLog(w http.ResponseWriter, req *http.Request) {
	limit := clampInt(req.URL.Query().Get("limit"), 50, 1, 500)
	list, err := s.Store.RecentActions(req.Context(), limit)
	if err != nil {
		httpError(w, s.Log, "list action log", err)
		return
	}
	writeJSON(w, map[string]any{"actions": list})
}

func (s *server) executeAction(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Kind   string         `json:"kind"`
		Params map[string]any `json:"params"`
	}
	if err := decodeJSON(req, &body); err != nil || body.Kind == "" {
		http.Error(w, "kind required", http.StatusBadRequest)
		return
	}
	ctx, done := s.detach(req)
	defer done()
	if err := s.Actions.Execute(ctx, body.Kind, body.Params); err != nil {
		httpError(w, s.Log, "execute action", err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *server) jellyfinScan(w http.ResponseWriter, req *http.Request) {
	ctx, done := s.detach(req)
	defer done()
	if err := s.Actions.JellyfinScan(ctx); err != nil {
		httpError(w, s.Log, "jellyfin scan", err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *server) retry(w http.ResponseWriter, req *http.Request) {
	var body struct {
		MediaItemID int64 `json:"media_item_id"`
	}
	if err := decodeJSON(req, &body); err != nil || body.MediaItemID == 0 {
		http.Error(w, "media_item_id required", http.StatusBadRequest)
		return
	}
	ctx, done := s.detach(req)
	defer done()
	if err := s.Actions.Retry(ctx, body.MediaItemID); err != nil {
		httpError(w, s.Log, "retry", err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *server) cancel(w http.ResponseWriter, req *http.Request) {
	var body struct {
		RequestID int64 `json:"request_id"`
	}
	if err := decodeJSON(req, &body); err != nil || body.RequestID == 0 {
		http.Error(w, "request_id required", http.StatusBadRequest)
		return
	}
	ctx, done := s.detach(req)
	defer done()
	if err := s.Actions.Cancel(ctx, body.RequestID); err != nil {
		httpError(w, s.Log, "cancel", err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// remove deletes a title everywhere. The body names it by request_id (the UI)
// or by media_type + tmdb_id/tvdb_id; plan_hash from the preview makes the
// call refuse when the plan changed in between (409, code plan_changed).
func (s *server) remove(w http.ResponseWriter, req *http.Request) {
	var body struct {
		actions.Title
		Force    bool   `json:"force"`
		PlanHash string `json:"plan_hash"`
	}
	if err := decodeJSON(req, &body); err != nil ||
		(body.RequestID == 0 && body.TmdbID == 0 && body.TvdbID == 0) {
		http.Error(w, "request_id or media_type with tmdb_id/tvdb_id required", http.StatusBadRequest)
		return
	}
	ctx, done := s.detach(req)
	defer done()
	steps, err := s.Actions.Remove(ctx, body.Title, body.PlanHash, body.Force)
	if err != nil {
		s.Log.Warn("remove", "title", body.Title, "err", err)
		status, code := http.StatusInternalServerError, "failed"
		switch {
		case errors.Is(err, actions.ErrBeingWatched):
			status, code = http.StatusConflict, "watching"
		case errors.Is(err, actions.ErrPlanChanged):
			status, code = http.StatusConflict, "plan_changed"
		case errors.Is(err, actions.ErrTitleNotFound):
			status, code = http.StatusNotFound, "not_found"
		}
		writeJSONStatus(w, status, map[string]any{"error": err.Error(), "code": code, "steps": steps})
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "steps": steps})
}
