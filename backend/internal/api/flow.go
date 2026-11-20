package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// flowKeys whitelists the settings a client may write via PUT /api/flow.
var flowKeys = map[string]bool{
	"auto_retry_stuck_after_secs": true,
	"notify_on_complete":          true,
	"notify_stage":                true,
}

// flowRoutes serve the control-plane settings (Flow menu).
func (s *server) flowRoutes(r chi.Router) {
	r.Get("/flow", s.getFlow)
	r.Put("/flow", s.putFlow)
}

func (s *server) getFlow(w http.ResponseWriter, req *http.Request) {
	m, err := s.Store.GetFlowSettings(req.Context())
	if err != nil {
		httpError(w, s.Log, "get flow settings", err)
		return
	}
	if m == nil {
		m = map[string]string{}
	}
	writeJSON(w, map[string]any{"settings": m})
}

func (s *server) putFlow(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Settings map[string]string `json:"settings"`
	}
	if err := decodeJSON(req, &body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	for k := range body.Settings {
		if !flowKeys[k] {
			http.Error(w, "unknown setting: "+k, http.StatusBadRequest)
			return
		}
	}
	for k, v := range body.Settings {
		if err := s.Store.SetFlowSetting(req.Context(), k, v); err != nil {
			httpError(w, s.Log, "set flow setting", err)
			return
		}
	}
	if s.Flow != nil {
		if err := s.Flow.Reload(req.Context()); err != nil {
			s.Log.Warn("flow: reload settings", "err", err)
		}
	}
	m, _ := s.Store.GetFlowSettings(req.Context())
	writeJSON(w, map[string]any{"settings": m})
}
