package ingest

import (
	"encoding/json"

	"github.com/pburkhalter/journarr/internal/pipeline"
)

// tdarrEvent is Journarr's Tdarr webhook contract. Configure a Tdarr flow's
// "Send Web Request" plugin to POST this to /webhook/tdarr?token=… on transcode
// start and/or complete. Tdarr reliably knows only the file path; the id fields
// are optional hints that improve matching when available.
type tdarrEvent struct {
	Event     string `json:"event"` // transcode.start | transcode.complete
	Phase     string `json:"phase"` // start | complete (alternative to event)
	File      string `json:"file"`
	MediaType string `json:"media_type"` // movie|tv (optional)
	TmdbID    int64  `json:"tmdb_id"`
	TvdbID    int64  `json:"tvdb_id"`
	Season    int64  `json:"season"`
	Episode   int64  `json:"episode"`
}

func (h *Handler) handleTdarr(body []byte) error {
	var t tdarrEvent
	if err := json.Unmarshal(body, &t); err != nil {
		return err
	}
	phase := t.Phase
	if phase == "" {
		switch t.Event {
		case "transcode.complete", "complete", "done":
			phase = "complete"
		default:
			phase = "start"
		}
	}
	op := pipeline.TranscodeOp{
		Phase: phase, File: t.File, MediaType: t.MediaType,
		TmdbID: t.TmdbID, TvdbID: t.TvdbID, Season: t.Season, Episode: t.Episode,
	}
	// Dedupe on file+phase so a retried webhook doesn't re-insert.
	dedupe := ""
	if t.File != "" {
		dedupe = "tdarr:" + phase + ":" + t.File
	}
	return h.emit("tdarr", "transcode", dedupe, op)
}
