package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// TVWaitingPoller is the TV analog of the movie ReleasePoller. A tv request is
// "waiting for release" when its Sonarr series has a future next-airing episode
// AND nothing is currently in flight (all items available/notified, or none
// yet) — i.e. there's nothing to do but wait for the next episode/season. It
// stamps the request with that date so it shows in the Waiting view; when work
// starts (a grab) or the series ends, the stamp clears. Airings change slowly,
// so it runs hourly.
type TVWaitingPoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Sonarr   *clients.Arr
	Interval time.Duration
	Publish  func(string, any)
}

func (p *TVWaitingPoller) Run(ctx context.Context) {
	if p.Interval <= 0 {
		p.Interval = time.Hour
	}
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

func (p *TVWaitingPoller) pass(ctx context.Context) {
	cands, err := p.Store.TVWaitingCandidates(ctx)
	if err != nil {
		p.Log.Warn("tv waiting: list candidates", "err", err)
		return
	}
	changed := map[int64]bool{}
	for _, c := range cands {
		if c.TvdbID == 0 {
			continue
		}
		s, err := p.Sonarr.SeriesByTvdbID(ctx, c.TvdbID)
		if err != nil {
			p.Log.Debug("tv waiting: sonarr lookup", "tvdb", c.TvdbID, "err", err)
			continue
		}
		var next *time.Time
		if s != nil && s.NextAiring != "" {
			if t, err := time.Parse(time.RFC3339, s.NextAiring); err == nil {
				next = &t
			}
		}
		// Waiting = a future next episode AND nothing being worked right now.
		waiting := next != nil && next.After(time.Now()) && !c.InFlight
		switch {
		case waiting:
			if c.AwaitingReleaseAt == nil || c.AwaitingReleaseAt.Format("2006-01-02") != next.Format("2006-01-02") {
				if err := p.Store.SetRequestAwaiting(ctx, c.ID, *next); err == nil {
					changed[c.ID] = true
				}
			}
		case c.AwaitingReleaseAt != nil: // no longer waiting → clear
			if err := p.Store.ClearRequestAwaiting(ctx, c.ID); err == nil {
				changed[c.ID] = true
			}
		}
	}
	if len(changed) > 0 {
		for id := range changed {
			if p.Publish != nil {
				p.Publish("request.updated", map[string]any{"id": id})
			}
		}
		p.Log.Info("tv waiting: refreshed", "requests", len(changed))
	}
}
