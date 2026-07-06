package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/store"
)

// SeerrRequestPoller reconciles the request lifecycle: anything the webhooks
// missed (downtime, disabled agent) converges via keyed events.
type SeerrRequestPoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Seerr    *clients.Seerr
	Interval time.Duration
	Wake     func()
}

func (p *SeerrRequestPoller) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	p.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pass(ctx)
		}
	}
}

func (p *SeerrRequestPoller) pass(ctx context.Context) {
	reqs, err := p.Seerr.RecentRequests(ctx, 50)
	if err != nil {
		p.Log.Warn("seerr poll: list requests", "err", err)
		return
	}
	inserted := 0
	for _, r := range reqs {
		kind := seerrStatusKind(r)
		if kind == "" {
			continue
		}
		op := pipeline.SeerrOp{
			SeerrRequestID: r.ID,
			Kind:           kind,
			MediaType:      r.Type,
			TmdbID:         r.Media.TmdbID,
			TvdbID:         r.Media.TvdbID,
			RequestedBy:    r.RequestedBy.DisplayName,
		}
		for _, s := range r.Seasons {
			op.Seasons = append(op.Seasons, s.SeasonNumber)
		}
		// Titles are not part of the request list; only fetch for requests
		// we have never seen (webhook path normally fills them).
		if known, err := p.Store.FindRequestBySeerrID(ctx, r.ID); err == nil && (known == nil || known.Title == "") {
			if title, year, poster, err := p.Seerr.MediaTitle(ctx, r.Type, r.Media.TmdbID); err == nil {
				op.Title, op.Year, op.Poster = title, year, poster
			}
		}
		dedupe := fmt.Sprintf("seerr:req:%d:%d:%d:%s", r.ID, r.Status, r.Media.Status, r.UpdatedAt)
		payload, _ := json.Marshal(op)
		if _, ok, err := p.Store.InsertEvent(ctx, "seerr", kind, dedupe, payload); err != nil {
			p.Log.Warn("seerr poll: insert event", "err", err)
		} else if ok {
			inserted++
		}
	}
	if inserted > 0 {
		p.Log.Info("seerr poll: new observations", "count", inserted)
		if p.Wake != nil {
			p.Wake()
		}
	}
}

// seerrStatusKind maps request.status + media.status onto the op kind.
func seerrStatusKind(r clients.SeerrRequest) string {
	if r.Media.Status >= 5 { // AVAILABLE
		return "available"
	}
	switch r.Status {
	case 1:
		return "pending"
	case 2:
		return "approved"
	case 3:
		return "declined"
	case 4:
		return "failed"
	case 5:
		return "available" // COMPLETED
	}
	return ""
}
