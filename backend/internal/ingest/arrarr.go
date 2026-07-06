package ingest

import (
	"encoding/json"
	"fmt"

	"github.com/pburkhalter/journarr/internal/pipeline"
)

// arrarrEvent is the outbound webhook Arrarr emits on job state transitions.
type arrarrEvent struct {
	Event           string `json:"event"`
	EventID         string `json:"event_id"`
	NzoID           string `json:"nzo_id"`
	NzbSHA256       string `json:"nzb_sha256"`
	Source          string `json:"source"`
	From            string `json:"from"`
	To              string `json:"to"`
	Filename        string `json:"filename"`
	SizeBytes       int64  `json:"size_bytes"`
	BytesDownloaded int64  `json:"bytes_downloaded"`
	BytesTotal      int64  `json:"bytes_total"`
	LocalPath       string `json:"local_path"`
	LastError       string `json:"last_error"`
}

func (h *Handler) handleArrarr(body []byte) error {
	var p arrarrEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if p.Event != "job.transition" || p.To == "" {
		return fmt.Errorf("arrarr: unhandled event %q", p.Event)
	}
	if p.NzoID == "" && p.NzbSHA256 == "" {
		return fmt.Errorf("arrarr: no correlation id")
	}
	op := pipeline.JobTransitionOp{
		NzoID:           p.NzoID,
		NzbSHA256:       p.NzbSHA256,
		Source:          p.Source,
		From:            p.From,
		To:              p.To,
		Filename:        p.Filename,
		SizeBytes:       p.SizeBytes,
		BytesDownloaded: p.BytesDownloaded,
		BytesTotal:      p.BytesTotal,
		LocalPath:       p.LocalPath,
		LastError:       p.LastError,
	}
	// Dedupe on the emitter's event_id when present (retries).
	dedupe := ""
	if p.EventID != "" {
		dedupe = "arrarr:" + p.EventID
	}
	return h.emit("arrarr", "job.transition", dedupe, op)
}
