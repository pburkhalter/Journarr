package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/store"
)

func (s *server) requestRoutes(r chi.Router) {
	r.Get("/requests", s.listRequests)
	r.Get("/requests/{id}", s.getRequest)
	r.Get("/requests/{id}/removal", s.planRemoval)
	r.Get("/media/{id}/events", s.mediaEvents)
}

func (s *server) listRequests(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	limit := clampInt(q.Get("limit"), 50, 1, 200)
	page := clampInt(q.Get("page"), 1, 1, 10000)
	list, err := s.Store.ListRequests(req.Context(), q.Get("status"), q.Get("q"), limit, (page-1)*limit)
	if err != nil {
		httpError(w, s.Log, "list requests", err)
		return
	}
	writeJSON(w, map[string]any{"requests": list, "page": page, "limit": limit})
}

// ItemDetail is a media item with its stage history.
type ItemDetail struct {
	store.MediaItem
	Transitions []store.StageTransition `json:"transitions"`
}

// RequestDetail is one request with its items and downloads.
type RequestDetail struct {
	Request   *store.RequestRollup `json:"request"`
	Items     []ItemDetail         `json:"items"`
	Downloads []store.Download     `json:"downloads"`
}

// LoadRequestDetail assembles a request's rollup, items with transitions and
// downloads; nil when the request does not exist. Shared with the MCP server.
func LoadRequestDetail(ctx context.Context, st *store.Store, id int64) (*RequestDetail, error) {
	request, err := st.RollupForRequest(ctx, id)
	if err != nil || request == nil {
		return nil, err
	}
	items, err := st.ListItemsForRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	details := make([]ItemDetail, 0, len(items))
	for _, it := range items {
		ts, err := st.TransitionsForItem(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		details = append(details, ItemDetail{MediaItem: it, Transitions: ts})
	}
	downloads, err := st.DownloadsForRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RequestDetail{Request: request, Items: details, Downloads: downloads}, nil
}

func (s *server) getRequest(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	d, err := LoadRequestDetail(req.Context(), s.Store, id)
	if err != nil {
		httpError(w, s.Log, "get request", err)
		return
	}
	if d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

// planRemoval shows what "Remove everywhere" would delete, for the
// confirmation dialog; its plan_hash goes back with the remove call.
func (s *server) planRemoval(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	plan, err := s.Actions.PlanRemoval(req.Context(), actions.Title{RequestID: id})
	if err != nil {
		if r, _ := s.Store.GetRequest(req.Context(), id); r == nil || errors.Is(err, actions.ErrTitleNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		httpError(w, s.Log, "plan removal", err)
		return
	}
	writeJSON(w, plan)
}

func (s *server) mediaEvents(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	events, err := s.Store.EventsForMedia(req.Context(), id, 100)
	if err != nil {
		httpError(w, s.Log, "list events", err)
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
}
