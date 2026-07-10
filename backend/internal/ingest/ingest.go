// Package ingest receives webhooks from Seerr, Sonarr and Radarr, normalizes
// them into pipeline ops and appends them to the events table. Responses are
// always fast-200 (processing is the projector's job); upstream retries are
// noisy and uninformative.
package ingest

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pburkhalter/journarr/internal/store"
)

const maxBody = 1 << 20 // 1 MiB

type Handler struct {
	Store *store.Store
	Log   *slog.Logger
	Token string // empty = webhook ingestion disabled (503)
	Wake  func() // nudges the projector
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/webhook/seerr", h.wrap(h.handleSeerr))
	r.Post("/webhook/sonarr", h.wrap(h.handleSonarr))
	r.Post("/webhook/radarr", h.wrap(h.handleRadarr))
	r.Post("/webhook/arrarr", h.wrap(h.handleArrarr))
	r.Post("/webhook/concierge", h.wrap(h.handleConcierge))
	r.Post("/webhook/tdarr", h.wrap(h.handleTdarr))
}

func (h *Handler) wrap(fn func(body []byte) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Token == "" {
			http.Error(w, "webhook ingestion disabled", http.StatusServiceUnavailable)
			return
		}
		// Header preferred (registered via the arrs' headers field / Seerr
		// customHeaders); query fallback for tools that only take a URL.
		token := r.Header.Get("X-Webhook-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.Token)) != 1 {
			h.Log.Warn("ingest: rejected webhook with bad token",
				"path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		// Respond immediately; normalization is cheap and done inline, the
		// heavy lifting happens in the projector goroutine.
		if err := fn(body); err != nil {
			h.Log.Warn("ingest: payload not usable", "err", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) emit(source, kind, dedupe string, op any) error {
	payload, err := json.Marshal(op)
	if err != nil {
		return err
	}
	_, inserted, err := h.Store.InsertEvent(nil2ctx(), source, kind, dedupe, payload)
	if err != nil {
		return err
	}
	if inserted && h.Wake != nil {
		h.Wake()
	}
	return nil
}
