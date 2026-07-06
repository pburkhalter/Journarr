package pipeline

import (
	"context"

	"github.com/pburkhalter/journarr/internal/store"
)

// applySeerr folds a request lifecycle signal into the request + items.
func (p *Projector) applySeerr(ctx context.Context, eventID int64, op SeerrOp) (string, int64, int64, int64) {
	if op.SeerrRequestID == 0 {
		return "ignored", 0, 0, 0
	}
	seerrID := op.SeerrRequestID
	seasons := seasonsJSON(op.Seasons)
	reqID, err := p.Store.UpsertRequest(ctx, store.Request{
		SeerrRequestID: &seerrID,
		MediaType:      op.MediaType,
		TmdbID:         nz(op.TmdbID),
		TvdbID:         nz(op.TvdbID),
		Title:          op.Title,
		Year:           nz(op.Year),
		PosterURL:      op.Poster,
		RequestedBy:    op.RequestedBy,
		Seasons:        seasons,
	})
	if err != nil {
		p.Log.Error("seerr: upsert request", "seerr_id", op.SeerrRequestID, "err", err)
		return "ignored", 0, 0, 0
	}
	p.touch(reqID)

	switch op.Kind {
	case "pending":
		if op.MediaType == "movie" {
			itemID := p.ensureMovieItem(ctx, reqID, op)
			p.apply(ctx, itemID, reqID, eventID, "requested", "seerr request")
			return "matched", reqID, itemID, 0
		}
		// tv: items appear at fan-out time (approval)
		return "matched", reqID, 0, 0

	case "approved":
		if op.MediaType == "movie" {
			itemID := p.ensureMovieItem(ctx, reqID, op)
			p.apply(ctx, itemID, reqID, eventID, "requested", "")
			p.apply(ctx, itemID, reqID, eventID, "approved", "seerr approval")
			return "matched", reqID, itemID, 0
		}
		p.fanoutTV(ctx, reqID, op.TvdbID, op.Seasons, eventID)
		return "matched", reqID, 0, 0

	case "declined":
		if err := p.Store.SetRequestStatus(ctx, reqID, "canceled"); err != nil {
			p.Log.Warn("seerr: set canceled", "request", reqID, "err", err)
		}
		return "matched", reqID, 0, 0

	case "available":
		items, err := p.Store.ListItemsForRequest(ctx, reqID)
		if err != nil {
			p.Log.Warn("seerr: list items", "request", reqID, "err", err)
			return "matched", reqID, 0, 0
		}
		if op.MediaType == "movie" && len(items) == 0 {
			p.ensureMovieItem(ctx, reqID, op)
			items, _ = p.Store.ListItemsForRequest(ctx, reqID)
		}
		if op.MediaType == "tv" && len(items) == 0 {
			// Nothing was ever tracked (backfill / pre-Journarr request) but
			// Seerr says it's available — close it out instead of leaving a
			// zero-item request "active" forever.
			if err := p.Store.SetRequestStatus(ctx, reqID, "completed"); err != nil {
				p.Log.Warn("seerr: complete empty tv request", "request", reqID, "err", err)
			}
			return "matched", reqID, 0, 0
		}
		for _, it := range items {
			p.apply(ctx, it.ID, reqID, eventID, "available", "seerr MEDIA_AVAILABLE")
		}
		return "matched", reqID, 0, 0

	case "failed":
		items, _ := p.Store.ListItemsForRequest(ctx, reqID)
		for _, it := range items {
			if it.CurrentStage != "available" && it.CurrentStage != "notified" {
				_ = p.Store.SetItemError(ctx, it.ID, "Seerr: processing failed")
			}
		}
		return "matched", reqID, 0, 0

	default:
		return "ignored", reqID, 0, 0
	}
}

func (p *Projector) ensureMovieItem(ctx context.Context, reqID int64, op SeerrOp) int64 {
	itemID, err := p.Store.EnsureMediaItem(ctx, store.MediaItem{
		RequestID: &reqID,
		MediaType: "movie",
		TmdbID:    nz(op.TmdbID),
		Title:     op.Title,
	})
	if err != nil {
		p.Log.Error("seerr: ensure movie item", "request", reqID, "err", err)
		return 0
	}
	return itemID
}

// apply is the SSE-publishing wrapper around store.ApplyStage using the
// item's current cycle.
func (p *Projector) apply(ctx context.Context, itemID, reqID, eventID int64, stage, note string) {
	if itemID == 0 {
		return
	}
	item, err := p.Store.GetMediaItem(ctx, itemID)
	if err != nil || item == nil {
		return
	}
	applied, err := p.Store.ApplyStage(ctx, itemID, item.CurrentCycle, stage, eventID, note)
	if err != nil {
		p.Log.Error("apply stage", "item", itemID, "stage", stage, "err", err)
		return
	}
	if applied {
		p.publishStage(itemID, reqID, stage, item.CurrentCycle)
		p.touch(reqID)
	}
}

func nz(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func seasonsJSON(seasons []int64) string {
	if len(seasons) == 0 {
		return ""
	}
	out := "["
	for i, s := range seasons {
		if i > 0 {
			out += ","
		}
		out += itoa64(s)
	}
	return out + "]"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
