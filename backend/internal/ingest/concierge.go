package ingest

import (
	"encoding/json"
	"fmt"

	"github.com/pburkhalter/journarr/internal/pipeline"
)

// conciergeEvent is the callback waha-concierge fires after a successful
// WhatsApp send.
type conciergeEvent struct {
	Event     string `json:"event"`
	MediaType string `json:"media_type"` // movie|tv
	TmdbID    int64  `json:"tmdb_id"`
	Title     string `json:"title"`
	Episodes  []struct {
		Season  int64 `json:"season"`
		Episode int64 `json:"episode"`
	} `json:"episodes"`
}

func (h *Handler) handleConcierge(body []byte) error {
	var p conciergeEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if p.Event != "notification.sent" || p.TmdbID == 0 {
		return fmt.Errorf("concierge: unhandled event %q", p.Event)
	}
	op := pipeline.NotifiedOp{MediaType: p.MediaType, TmdbID: p.TmdbID, Title: p.Title}
	for _, e := range p.Episodes {
		op.Episodes = append(op.Episodes, pipeline.EpisodeNum{Season: e.Season, Episode: e.Episode})
	}
	// No dedupe key: concierge sends once per batch; applying 'notified' is
	// idempotent per item/cycle anyway.
	return h.emit("concierge", "notified", "", op)
}
