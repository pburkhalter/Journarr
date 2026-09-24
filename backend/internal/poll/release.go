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
// of a stall, and the stuck sweeper skips them. It clears the flag once the
// film is out (or on disk). Release dates change slowly, so it runs hourly.
type ReleasePoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Radarr   *clients.Arr
	Interval time.Duration
	Publish  func(string, any)
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
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
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	changed := map[int64]bool{}
	for _, it := range items {
		if it.TmdbID == nil {
			continue
		}
		mv, err := p.Radarr.LookupMovieByTmdb(ctx, *it.TmdbID)
		if err != nil {
			p.Log.Warn("release poll: radarr lookup", "tmdb", *it.TmdbID, "err", err)
			continue
		}
		wasAwaiting := it.AwaitingReleaseAt != nil
		when, waiting := releaseWait(mv, now)
		if !waiting {
			if wasAwaiting {
				if err := p.Store.ClearAwaitingRelease(ctx, it.ID); err == nil && it.RequestID != nil {
					changed[*it.RequestID] = true
				}
			}
			continue
		}
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

// releaseWait decides whether a film is genuinely not out yet, from its dates
// rather than from Radarr's isAvailable alone. isAvailable is a function of
// the profile's minimumAvailability: with "announced" every film reads as
// available the moment it is added, which turned every unreleased request
// into a stuck item. A future date wins over that flag; the flag still counts
// when the film is out but the profile keeps Radarr from searching yet.
func releaseWait(mv *clients.MovieRelease, now time.Time) (*time.Time, bool) {
	if mv == nil || mv.HasFile {
		return nil, false
	}
	when := expectedRelease(mv)
	switch {
	case when == nil:
		return nil, true // TBA: nothing to search for yet
	case when.After(now):
		return when, true
	case !mv.IsAvailable:
		return when, true // out, but below minimumAvailability: Radarr won't search
	}
	return nil, false
}

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
