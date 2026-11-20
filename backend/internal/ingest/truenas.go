package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// pushoverMaxMessage is Pushover's limit for the message text, in characters.
const pushoverMaxMessage = 1024

// handleTruenas relays TrueNAS alerts. TrueNAS 25.10 has no Pushover alert
// service, so its Slack service posts {"text": "..."} here (token in the
// URL) and the text goes out through the same Pushover sender as Journarr's
// own health alerts. Nothing is stored: it is a notification, not a
// pipeline event.
func (h *Handler) handleTruenas(body []byte) error {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return errors.New("truenas: empty alert text")
	}
	if r := []rune(text); len(r) > pushoverMaxMessage {
		text = string(r[:pushoverMaxMessage-1]) + "…"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := h.Relay(ctx, "TrueNAS", text); err != nil {
		h.Log.Error("ingest: truenas relay failed", "err", err)
		return nil
	}
	h.Log.Info("ingest: truenas alert relayed", "bytes", len(text))
	return nil
}
