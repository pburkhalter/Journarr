package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

type Projector struct {
	Store   *store.Store
	Log     *slog.Logger
	Publish func(event string, data any)
	Sonarr  *clients.Arr // nil when not configured
	Radarr  *clients.Arr

	wake chan struct{}

	mu        sync.Mutex
	retries   map[int64]*fanoutRetry // request id -> pending tv fan-out
	nextSweep time.Time              // durable fan-out sweep (DB-backed)

	// affected requests within one drain pass, flushed as request.updated
	touched map[int64]struct{}
}

type fanoutRetry struct {
	tvdbID   int64
	seasons  []int64
	attempts int
	nextAt   time.Time
}

func New(st *store.Store, log *slog.Logger, publish func(string, any), sonarr, radarr *clients.Arr) *Projector {
	return &Projector{
		Store: st, Log: log, Publish: publish,
		Sonarr: sonarr, Radarr: radarr,
		wake:    make(chan struct{}, 1),
		retries: map[int64]*fanoutRetry{},
	}
}

// Wake nudges the projector; safe from any goroutine.
func (p *Projector) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Projector) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-tick.C:
		}
		p.drain(ctx)
		p.retryFanouts(ctx)
	}
}

func (p *Projector) drain(ctx context.Context) {
	p.touched = map[int64]struct{}{}
	// Flush even on early error returns — otherwise request.updated events
	// and status rollups for already-applied work would be dropped.
	defer p.flushTouched(ctx)
	for {
		events, err := p.Store.FetchUnprocessed(ctx, 100)
		if err != nil {
			p.Log.Error("projector: fetch events", "err", err)
			return
		}
		if len(events) == 0 {
			break
		}
		for _, ev := range events {
			match, reqID, itemID, dlID := p.handle(ctx, ev)
			if err := p.Store.MarkProcessed(ctx, ev.ID, match, reqID, itemID, dlID); err != nil {
				p.Log.Error("projector: mark processed", "event", ev.ID, "err", err)
				return // retry the whole batch next wake
			}
		}
		if len(events) < 100 {
			break
		}
	}
}

func (p *Projector) touch(requestID int64) {
	if requestID > 0 && p.touched != nil {
		p.touched[requestID] = struct{}{}
	}
}

func (p *Projector) flushTouched(ctx context.Context) {
	for reqID := range p.touched {
		status, err := p.Store.RecomputeRequestStatus(ctx, reqID)
		if err != nil {
			p.Log.Warn("projector: recompute status", "request", reqID, "err", err)
			continue
		}
		if p.Publish != nil {
			p.Publish("request.updated", map[string]any{"id": reqID, "status": status})
		}
	}
	p.touched = nil
}

// handle routes one event; it never fails the pipeline — parse errors mark
// the event ignored and move on.
func (p *Projector) handle(ctx context.Context, ev store.Event) (match string, reqID, itemID, dlID int64) {
	defer func() {
		if r := recover(); r != nil {
			p.Log.Error("projector: panic in handler", "event", ev.ID, "kind", ev.Kind, "panic", r)
			match = "ignored"
		}
	}()

	switch {
	case ev.Source == "seerr":
		var op SeerrOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			p.Log.Warn("projector: bad seerr op", "event", ev.ID, "err", err)
			return "ignored", 0, 0, 0
		}
		return p.applySeerr(ctx, ev.ID, op)

	case ev.Kind == "grab":
		var op GrabOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			return "ignored", 0, 0, 0
		}
		return p.applyGrab(ctx, ev.ID, op)

	case ev.Kind == "import":
		var op ImportOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			return "ignored", 0, 0, 0
		}
		return p.applyImport(ctx, ev.ID, op)

	case ev.Kind == "failure":
		var op FailureOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			return "ignored", 0, 0, 0
		}
		return p.applyFailure(ctx, ev.ID, op)

	case ev.Kind == "job.transition":
		var op JobTransitionOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			return "ignored", 0, 0, 0
		}
		return p.applyJobTransition(ctx, ev.ID, op)

	case ev.Kind == "available":
		var op AvailableOp
		if err := json.Unmarshal(ev.Payload, &op); err != nil {
			return "ignored", 0, 0, 0
		}
		return p.applyAvailable(ctx, ev.ID, op)

	default:
		p.Log.Debug("projector: unhandled event", "source", ev.Source, "kind", ev.Kind)
		return "ignored", 0, 0, 0
	}
}

func (p *Projector) publishStage(itemID, requestID int64, stage string, cycle int) {
	if p.Publish != nil {
		p.Publish("media.stage", map[string]any{
			"media_item_id": itemID, "request_id": requestID, "stage": stage, "cycle": cycle,
		})
	}
}
