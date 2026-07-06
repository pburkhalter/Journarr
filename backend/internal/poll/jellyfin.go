package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/store"
)

// JellyfinPoller detects newly-available library items and marks the matching
// tracked media items 'available'. Matching happens here (it needs Jellyfin
// series lookups); the projector just applies the resolved stage.
type JellyfinPoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Jelly    *clients.Jellyfin
	Interval time.Duration
	Wake     func()
}

func (p *JellyfinPoller) Run(ctx context.Context) {
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

func (p *JellyfinPoller) pass(ctx context.Context) {
	items, err := p.Jelly.RecentlyAdded(ctx, 100)
	if err != nil {
		p.Log.Warn("jellyfin poll: recently added", "err", err)
		return
	}
	seriesTvdb := map[string]int64{} // SeriesId -> tvdb, cached per pass (success only)
	inserted := 0

	emit := func(mediaItemID int64, jellyID string) {
		item, err := p.Store.GetMediaItem(ctx, mediaItemID)
		if err != nil || item == nil {
			return
		}
		// Already available/notified in the current cycle — nothing to do.
		if item.CurrentStage == "available" || item.CurrentStage == "notified" {
			return
		}
		op := pipeline.AvailableOp{MediaItemID: mediaItemID, JellyfinItemID: jellyID}
		payload, _ := json.Marshal(op)
		// Cycle-scoped dedupe: a re-grab/upgrade bumps the cycle, and the file
		// re-appearing in Jellyfin must be able to re-mark the new cycle
		// available (an item-only key would block that permanently).
		dedupe := fmt.Sprintf("jellyfin:avail:%d:%d", mediaItemID, item.CurrentCycle)
		if _, ok, err := p.Store.InsertEvent(ctx, "jellyfin", "available", dedupe, payload); err != nil {
			p.Log.Warn("jellyfin poll: insert", "err", err)
		} else if ok {
			inserted++
		}
	}

	for _, it := range items {
		switch it.Type {
		case "Movie":
			tmdb := providerID(it.ProviderIds, "Tmdb")
			if tmdb == 0 {
				continue
			}
			if mi, err := p.Store.FindMovieItemByTmdb(ctx, tmdb); err == nil && mi != nil {
				emit(mi.ID, it.ID)
			}
		case "Episode":
			if it.ParentIndexNumber == nil || it.IndexNumber == nil || it.SeriesID == "" {
				continue
			}
			tvdb, ok := seriesTvdb[it.SeriesID]
			if !ok {
				v, e := p.Jelly.SeriesTvdbID(ctx, it.SeriesID)
				if e != nil {
					// Don't cache a transient failure — retry on the next
					// episode/pass rather than skipping the whole series.
					p.Log.Debug("jellyfin poll: series tvdb lookup", "series", it.SeriesID, "err", e)
					continue
				}
				seriesTvdb[it.SeriesID] = v // cache success (incl. a genuine 0)
				tvdb = v
			}
			if tvdb == 0 {
				continue
			}
			season := *it.ParentIndexNumber
			epStart := *it.IndexNumber
			epEnd := epStart
			// Multi-episode files (IndexNumberEnd) cover a range.
			if it.IndexNumberEnd != nil && *it.IndexNumberEnd > epEnd {
				epEnd = *it.IndexNumberEnd
			}
			for ep := epStart; ep <= epEnd; ep++ {
				if mi, err := p.Store.FindEpisodeItemByTvdb(ctx, tvdb, season, ep); err == nil && mi != nil {
					emit(mi.ID, it.ID)
				}
			}
		}
	}
	if inserted > 0 {
		p.Log.Info("jellyfin poll: newly available", "count", inserted)
		if p.Wake != nil {
			p.Wake()
		}
	}
}

// providerID reads a provider id case-insensitively (Jellyfin keys are
// PascalCase "Tmdb"/"Tvdb", but be tolerant to match the series-side lookup).
func providerID(ids map[string]string, key string) int64 {
	for k, v := range ids {
		if !strings.EqualFold(k, key) {
			continue
		}
		var n int64
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int64(ch-'0')
		}
		return n
	}
	return 0
}
