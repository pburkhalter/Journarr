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

// PresencePoller reconciles tracked items against the arr's ground truth:
// content that is already on disk (episode/movie hasFile) but sits below
// 'available' — because it pre-dates Journarr or aged out of the Jellyfin
// recent-added window — is advanced to 'available'. This is what stops a
// long-present series showing as "0/88" stuck at approved.
//
// Rationale for 'available' (not 'imported'): an arr hasFile means the file
// lives in the library path Jellyfin serves, so it is watchable. The note
// records that this was reconciled from the arr, not observed in Jellyfin.
type PresencePoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Sonarr   *clients.Arr // nil = tv reconciliation skipped
	Radarr   *clients.Arr // nil = movie reconciliation skipped
	Interval time.Duration
	Wake     func()
}

func (p *PresencePoller) Run(ctx context.Context) {
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

func (p *PresencePoller) pass(ctx context.Context) {
	items, err := p.Store.ListUnavailableActiveItems(ctx)
	if err != nil {
		p.Log.Warn("presence poll: list items", "err", err)
		return
	}
	if len(items) == 0 {
		return
	}

	// Group episode items by Sonarr series so we fetch each series once.
	bySeries := map[int64][]store.MediaItem{}
	var movies []store.MediaItem
	for _, it := range items {
		switch {
		case it.MediaType == "episode" && it.SonarrSeriesID != nil && p.Sonarr != nil:
			bySeries[*it.SonarrSeriesID] = append(bySeries[*it.SonarrSeriesID], it)
		case it.MediaType == "movie" && it.RadarrMovieID != nil && p.Radarr != nil:
			movies = append(movies, it)
		}
	}

	inserted := 0
	emit := func(itemID int64, cycle int) {
		op := pipeline.AvailableOp{MediaItemID: itemID, Note: "present in library (reconciled)"}
		payload, _ := json.Marshal(op)
		dedupe := fmt.Sprintf("presence:avail:%d:%d", itemID, cycle)
		if _, ok, err := p.Store.InsertEvent(ctx, "presence", "available", dedupe, payload); err != nil {
			p.Log.Warn("presence poll: insert", "err", err)
		} else if ok {
			inserted++
		}
	}

	for seriesID, group := range bySeries {
		eps, err := p.Sonarr.EpisodesBySeries(ctx, seriesID)
		if err != nil {
			p.Log.Debug("presence poll: sonarr episodes", "series", seriesID, "err", err)
			continue
		}
		hasFile := make(map[int64]bool, len(eps))
		for _, e := range eps {
			hasFile[e.ID] = e.HasFile
		}
		for _, it := range group {
			if it.SonarrEpisodeID != nil && hasFile[*it.SonarrEpisodeID] {
				emit(it.ID, it.CurrentCycle)
			}
		}
	}

	for _, it := range movies {
		mv, err := p.Radarr.MovieByID(ctx, *it.RadarrMovieID)
		if err != nil {
			p.Log.Debug("presence poll: radarr movie", "movie", *it.RadarrMovieID, "err", err)
			continue
		}
		if mv.HasFile {
			emit(it.ID, it.CurrentCycle)
		}
	}

	if inserted > 0 {
		p.Log.Info("presence poll: reconciled present items to available", "count", inserted)
		if p.Wake != nil {
			p.Wake()
		}
	}
}
