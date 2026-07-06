package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pburkhalter/journarr/internal/pipeline"
)

// seerrWebhook is a tolerant subset of the delivered default template.
// Seerr substitutes template variables as STRINGS ("tmdbId": "12345").
type seerrWebhook struct {
	NotificationType string `json:"notification_type"`
	Subject          string `json:"subject"`
	Image            string `json:"image"`
	Media            *struct {
		MediaType string `json:"media_type"`
		TmdbID    string `json:"tmdbId"`
		TvdbID    string `json:"tvdbId"`
		Status    string `json:"status"`
	} `json:"media"`
	Request *struct {
		RequestID           string `json:"request_id"`
		RequestedByUsername string `json:"requestedBy_username"`
		RequestedByEmail    string `json:"requestedBy_email"`
	} `json:"request"`
	Extra []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"extra"`
}

var subjectYear = regexp.MustCompile(`^(.*)\s\((\d{4})\)$`)

func (h *Handler) handleSeerr(body []byte) error {
	var p seerrWebhook
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if p.NotificationType == "TEST_NOTIFICATION" {
		h.Log.Info("ingest: seerr test notification received")
		return nil
	}
	kind := map[string]string{
		"MEDIA_PENDING":        "pending",
		"MEDIA_AUTO_REQUESTED": "pending",
		"MEDIA_APPROVED":       "approved",
		"MEDIA_AUTO_APPROVED":  "approved",
		"MEDIA_DECLINED":       "declined",
		"MEDIA_AVAILABLE":      "available",
		"MEDIA_FAILED":         "failed",
	}[p.NotificationType]
	if kind == "" || p.Request == nil || p.Media == nil {
		return fmt.Errorf("seerr: unhandled type %q", p.NotificationType)
	}
	reqID, _ := strconv.ParseInt(p.Request.RequestID, 10, 64)
	if reqID == 0 {
		return fmt.Errorf("seerr: no request_id")
	}

	op := pipeline.SeerrOp{
		SeerrRequestID: reqID,
		Kind:           kind,
		MediaType:      p.Media.MediaType,
		Poster:         p.Image,
		RequestedBy:    p.Request.RequestedByUsername,
	}
	op.TmdbID, _ = strconv.ParseInt(p.Media.TmdbID, 10, 64)
	op.TvdbID, _ = strconv.ParseInt(p.Media.TvdbID, 10, 64)

	title := p.Subject
	if m := subjectYear.FindStringSubmatch(p.Subject); m != nil {
		title = m[1]
		op.Year, _ = strconv.ParseInt(m[2], 10, 64)
	}
	op.Title = title

	for _, e := range p.Extra {
		if strings.EqualFold(e.Name, "Requested Seasons") {
			for _, part := range strings.Split(e.Value, ",") {
				if n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
					op.Seasons = append(op.Seasons, n)
				}
			}
		}
	}
	// No dedupe key: Seerr sends each notification once; the request poller
	// uses its own keyed events.
	return h.emit("seerr", kind, "", op)
}

func nil2ctx() context.Context { return context.Background() }
