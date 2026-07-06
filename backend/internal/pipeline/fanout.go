package pipeline

import (
	"context"
	"time"

	"github.com/pburkhalter/journarr/internal/store"
)

// fanoutTV expands a tv request into one media item per aired, monitored
// episode of the requested seasons. Idempotent (EnsureMediaItem). When the
// series is not in Sonarr yet (Seerr just pushed it), the fan-out is queued
// for retry.
func (p *Projector) fanoutTV(ctx context.Context, reqID int64, tvdbID int64, seasons []int64, eventID int64) {
	if p.Sonarr == nil || tvdbID == 0 {
		return
	}
	series, err := p.Sonarr.SeriesByTvdbID(ctx, tvdbID)
	if err != nil || series == nil {
		p.queueFanoutRetry(reqID, tvdbID, seasons)
		if err != nil {
			p.Log.Warn("fanout: sonarr lookup failed, queued retry", "tvdb", tvdbID, "err", err)
		} else {
			p.Log.Info("fanout: series not in sonarr yet, queued retry", "tvdb", tvdbID)
		}
		return
	}
	episodes, err := p.Sonarr.EpisodesBySeries(ctx, series.ID)
	if err != nil {
		p.queueFanoutRetry(reqID, tvdbID, seasons)
		p.Log.Warn("fanout: episode list failed, queued retry", "series", series.ID, "err", err)
		return
	}

	want := map[int64]bool{}
	for _, s := range seasons {
		want[s] = true
	}
	now := time.Now()
	created := 0
	for _, ep := range episodes {
		if len(want) > 0 && !want[ep.SeasonNumber] {
			continue
		}
		if !ep.Monitored {
			continue
		}
		// Unaired episodes are skipped here; they attach automatically when
		// their grab arrives (applyGrab creates missing items on demand).
		if ep.AirDateUTC == "" {
			continue
		}
		if aired, err := time.Parse(time.RFC3339, ep.AirDateUTC); err != nil || aired.After(now.Add(24*time.Hour)) {
			continue
		}
		season, episode := ep.SeasonNumber, ep.EpisodeNumber
		epID := ep.ID
		seriesID := series.ID
		itemID, err := p.Store.EnsureMediaItem(ctx, store.MediaItem{
			RequestID:       &reqID,
			MediaType:       "episode",
			TvdbID:          &series.TvdbID,
			SonarrSeriesID:  &seriesID,
			SonarrEpisodeID: &epID,
			SeasonNumber:    &season,
			EpisodeNumber:   &episode,
			Title:           ep.Title,
		})
		if err != nil {
			p.Log.Error("fanout: ensure item", "episode", ep.ID, "err", err)
			continue
		}
		p.apply(ctx, itemID, reqID, eventID, "requested", "")
		p.apply(ctx, itemID, reqID, eventID, "approved", "fanout")
		created++
	}
	p.Log.Info("fanout: done", "request", reqID, "series", series.Title, "items", created)
	p.mu.Lock()
	delete(p.retries, reqID)
	p.mu.Unlock()
}

func (p *Projector) queueFanoutRetry(reqID, tvdbID int64, seasons []int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.retries[reqID]
	if !ok {
		r = &fanoutRetry{tvdbID: tvdbID, seasons: seasons}
		p.retries[reqID] = r
	}
	r.nextAt = time.Now().Add(30 * time.Second)
}

// retryFanouts runs after each drain. The in-memory queue covers the fast
// path; the DB sweep (every 5m) is the durable safety net — it survives
// restarts and the Seerr-poller dedupe (an unchanged request emits no new
// events, so nothing else would re-drive an abandoned fan-out).
func (p *Projector) retryFanouts(ctx context.Context) {
	p.mu.Lock()
	due := map[int64]*fanoutRetry{}
	now := time.Now()
	for reqID, r := range p.retries {
		if r.attempts > 120 {
			delete(p.retries, reqID)
			continue
		}
		if now.After(r.nextAt) {
			r.attempts++
			r.nextAt = now.Add(30 * time.Second)
			due[reqID] = r
		}
	}
	sweep := now.After(p.nextSweep)
	if sweep {
		p.nextSweep = now.Add(5 * time.Minute)
	}
	p.mu.Unlock()

	if sweep {
		reqs, err := p.Store.ActiveTVRequestsWithoutItems(ctx)
		if err != nil {
			p.Log.Warn("fanout sweep: query", "err", err)
		}
		for _, r := range reqs {
			if _, queued := due[r.ID]; queued {
				continue
			}
			if r.TvdbID != nil {
				due[r.ID] = &fanoutRetry{tvdbID: *r.TvdbID, seasons: parseSeasons(r.Seasons)}
			}
		}
	}

	for reqID, r := range due {
		p.touched = map[int64]struct{}{}
		p.fanoutTV(ctx, reqID, r.tvdbID, r.seasons, 0)
		p.flushTouched(ctx)
	}
}

// parseSeasons decodes the request's seasons JSON array ("[1,2]").
func parseSeasons(s string) []int64 {
	var out []int64
	num := int64(-1)
	for _, ch := range s {
		switch {
		case ch >= '0' && ch <= '9':
			if num < 0 {
				num = 0
			}
			num = num*10 + int64(ch-'0')
		default:
			if num >= 0 {
				out = append(out, num)
				num = -1
			}
		}
	}
	if num >= 0 {
		out = append(out, num)
	}
	return out
}
