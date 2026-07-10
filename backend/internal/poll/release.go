package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// ReleasePoller flags requested/approved movies that Radarr can't grab yet
// because they aren't released, annotating them with the expected availability
// date (awaiting_release_at). The UI then shows "waiting for release" instead
// of a stall, and the stuck sweeper skips them. It clears the flag once Radarr
// reports the film available. Release dates change slowly, so it runs hourly.
type ReleasePoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Radarr   *clients.Arr
	Interval time.Duration
	Publish  func(string, any)
}

func (p *ReleasePoller) Run(ctx context.Context) {
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

func (p *ReleasePoller) pass(ctx context.Context) {
	items, err := p.Store.MoviesForReleaseCheck(ctx)
	if err != nil {
		p.Log.Warn("release poll: list movies", "err", err)
		return
	}
	changed := map[int64]bool{}
	for _, it := range items {
		if it.TmdbID == nil {
			continue
		}
		mv, err := p.Radarr.LookupMovieByTmdb(ctx, *it.TmdbID)
		if err != nil {
			p.Log.Debug("release poll: radarr lookup", "tmdb", *it.TmdbID, "err", err)
			continue
		}
		wasAwaiting := it.AwaitingReleaseAt != nil
		// Not in Radarr (yet) or already available → not waiting for release.
		if mv == nil || mv.IsAvailable {
			if wasAwaiting {
				if err := p.Store.ClearAwaitingRelease(ctx, it.ID); err == nil && it.RequestID != nil {
					changed[*it.RequestID] = true
				}
			}
			continue
		}
		when := expectedRelease(mv)
		if when == nil {
			// Radarr has no date yet (announced/TBA). Flag once with a sentinel
			// far-future date the UI renders as "date unknown"; don't rewrite it
			// each pass.
			if !wasAwaiting {
				if err := p.Store.SetAwaitingRelease(ctx, it.ID, tbaSentinel); err == nil && it.RequestID != nil {
					changed[*it.RequestID] = true
				}
			}
			continue
		}
		// Skip a redundant write when the date is unchanged (day granularity).
		if wasAwaiting && it.AwaitingReleaseAt.Format("2006-01-02") == when.Format("2006-01-02") {
			continue
		}
		if err := p.Store.SetAwaitingRelease(ctx, it.ID, *when); err != nil {
			p.Log.Warn("release poll: set awaiting", "item", it.ID, "err", err)
			continue
		}
		if it.RequestID != nil {
			changed[*it.RequestID] = true
		}
	}
	if len(changed) > 0 {
		for reqID := range changed {
			if p.Publish != nil {
				p.Publish("request.updated", map[string]any{"id": reqID})
			}
		}
		p.Log.Info("release poll: refreshed waiting-for-release", "requests", len(changed))
	}
}

// tbaSentinel marks "waiting for release, date unknown" (Radarr has no release
// date yet). The UI recognizes the far-future year and shows no specific date.
var tbaSentinel = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// expectedRelease picks the soonest date at which Radarr expects the film to
// become grabbable: digital, else physical, else (as a rough signal) cinema.
func expectedRelease(mv *clients.MovieRelease) *time.Time {
	for _, d := range []*time.Time{mv.DigitalRelease, mv.PhysicalRelease, mv.InCinemas} {
		if d != nil && !d.IsZero() {
			return d
		}
	}
	return nil
}
