package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/storage"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/updates"
)

func (s *server) serviceRoutes(r chi.Router) {
	r.Get("/services", s.listServices)
	r.Get("/instances", s.listInstances)
	r.Get("/diskspace", s.diskSpace)
	r.Get("/stages", s.listStages)
	r.Get("/sessions", s.jellyfinSessions)
	r.Get("/stats", s.stats)
}

// ServiceView is a health row merged with the GitHub update status.
type ServiceView struct {
	store.ServiceHealth
	Update *updates.Info `json:"update,omitempty"`
}

// Services returns every service's health with its update status. Shared by
// the API and the MCP server.
func Services(d Deps, req *http.Request) ([]ServiceView, error) {
	list, err := d.Store.ListServiceHealth(req.Context())
	if err != nil {
		return nil, err
	}
	out := make([]ServiceView, 0, len(list))
	for _, h := range list {
		v := ServiceView{ServiceHealth: h}
		if d.Updates != nil {
			if info, ok := d.Updates.Get(h.Service); ok {
				v.Update = &info
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *server) listServices(w http.ResponseWriter, req *http.Request) {
	out, err := Services(s.Deps, req)
	if err != nil {
		httpError(w, s.Log, "list services", err)
		return
	}
	writeJSON(w, map[string]any{"services": out})
}

// listInstances serves the registry metadata: tile ordering, labels and
// folding in the UI.
func (s *server) listInstances(w http.ResponseWriter, _ *http.Request) {
	var meta []registry.InstanceMeta
	if s.Registry != nil {
		meta = s.Registry.Meta()
	}
	writeJSON(w, map[string]any{"instances": meta})
}

// diskSpace aggregates free space per mount from whichever instances can
// report it. Host-agnostic on purpose — see package storage.
func (s *server) diskSpace(w http.ResponseWriter, req *http.Request) {
	mounts := []storage.Mount{}
	if s.Storage != nil {
		if m := s.Storage.Mounts(req.Context()); m != nil {
			mounts = m
		}
	}
	writeJSON(w, map[string]any{"mounts": mounts})
}

// listStages is the active pipeline stage catalog — the single source the
// frontend reads instead of re-declaring stages. Capability-gated stages
// (e.g. transcode) only appear when a providing instance exists.
func (s *server) listStages(w http.ResponseWriter, req *http.Request) {
	all, err := s.Store.ListStages(req.Context())
	if err != nil {
		httpError(w, s.Log, "list stages", err)
		return
	}
	out := make([]store.Stage, 0, len(all))
	for _, st := range all {
		if !st.Active || (s.Registry != nil && !s.Registry.StageActive(st.Key)) {
			continue
		}
		out = append(out, st)
	}
	writeJSON(w, map[string]any{"stages": out})
}

// jellyfinSessions lists live playback sessions — "who's streaming / safe
// to restart?".
func (s *server) jellyfinSessions(w http.ResponseWriter, req *http.Request) {
	sessions := []clients.JellySession{}
	if s.Registry != nil {
		if jf := s.Registry.Jellyfin(); jf != nil {
			list, err := jf.Sessions(req.Context())
			if err != nil {
				httpError(w, s.Log, "jellyfin sessions", err)
				return
			}
			sessions = list
		}
	}
	writeJSON(w, map[string]any{"sessions": sessions, "safe_to_restart": len(sessions) == 0})
}

func (s *server) stats(w http.ResponseWriter, req *http.Request) {
	st, err := s.Store.FetchStats(req.Context())
	if err != nil {
		httpError(w, s.Log, "fetch stats", err)
		return
	}
	stuck, _ := s.Store.CountStuck(req.Context())
	writeJSON(w, map[string]any{"requests": st.Requests, "media_items": st.MediaItems, "stuck": stuck})
}
