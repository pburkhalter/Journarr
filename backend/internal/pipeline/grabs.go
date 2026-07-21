package pipeline

import (
	"context"

	"github.com/pburkhalter/journarr/internal/store"
)

// resolveEpisodeItems maps grab/import episodes onto media items, creating
// missing ones attached to the active request for the series (or an orphan
// request when nobody asked for it via Seerr).
func (p *Projector) resolveEpisodeItems(ctx context.Context, series *SeriesRef, episodes []EpisodeRef) (map[int64]*store.MediaItem, int64) {
	out := map[int64]*store.MediaItem{}
	if series == nil {
		return out, 0
	}
	var reqID int64

	epIDs := make([]int64, 0, len(episodes))
	for _, ep := range episodes {
		epIDs = append(epIDs, ep.SonarrID)
	}
	existing, err := p.Store.FindItemsBySonarrEpisodeIDs(ctx, epIDs)
	if err != nil {
		p.Log.Error("resolve: lookup by episode ids", "err", err)
		return out, 0
	}

	for _, ep := range episodes {
		if item, ok := existing[ep.SonarrID]; ok {
			out[ep.SonarrID] = item
			if item.RequestID != nil {
				reqID = *item.RequestID
			}
			continue
		}
		// Missing: attach to the existing request for this series (any status —
		// a new season/upgrade after completion must not spawn a duplicate),
		// else orphan.
		if reqID == 0 {
			req, err := p.Store.FindRequestByTvdb(ctx, series.TvdbID)
			if err != nil {
				p.Log.Error("resolve: find request by tvdb", "err", err)
				continue
			}
			if req != nil {
				reqID = req.ID
			} else {
				reqID, err = p.Store.InsertOrphanRequest(ctx, "tv", series.Title, nz(series.TmdbID), nz(series.TvdbID))
				if err != nil {
					p.Log.Error("resolve: orphan request", "err", err)
					continue
				}
				p.Log.Info("resolve: created orphan tv request", "series", series.Title, "request", reqID)
			}
		}
		season, episode, epID, seriesID := ep.Season, ep.Episode, ep.SonarrID, series.SonarrID
		itemID, err := p.Store.EnsureMediaItem(ctx, store.MediaItem{
			RequestID:       &reqID,
			MediaType:       "episode",
			TvdbID:          nz(series.TvdbID),
			SonarrSeriesID:  &seriesID,
			SonarrEpisodeID: &epID,
			SeasonNumber:    &season,
			EpisodeNumber:   &episode,
			Title:           ep.Title,
		})
		if err != nil {
			p.Log.Error("resolve: ensure episode item", "err", err)
			continue
		}
		item, err := p.Store.GetMediaItem(ctx, itemID)
		if err != nil || item == nil {
			continue
		}
		out[ep.SonarrID] = item
	}
	return out, reqID
}

// resolveMovieItem finds or creates the item for a Radarr movie.
func (p *Projector) resolveMovieItem(ctx context.Context, movie *MovieRef) (*store.MediaItem, int64) {
	if movie == nil {
		return nil, 0
	}
	// Match regardless of status: an upgrade / re-grab arrives after the request
	// already completed — attach to it instead of spawning a duplicate orphan.
	req, err := p.Store.FindRequestByTmdb(ctx, movie.TmdbID, "movie")
	if err != nil {
		p.Log.Error("resolve: find movie request", "err", err)
		return nil, 0
	}
	var reqID int64
	if req != nil {
		reqID = req.ID
	} else {
		reqID, err = p.Store.InsertOrphanRequest(ctx, "movie", movie.Title, nz(movie.TmdbID), nil)
		if err != nil {
			p.Log.Error("resolve: orphan movie request", "err", err)
			return nil, 0
		}
		p.Log.Info("resolve: created orphan movie request", "movie", movie.Title, "request", reqID)
	}
	radarrID := movie.RadarrID
	itemID, err := p.Store.EnsureMediaItem(ctx, store.MediaItem{
		RequestID:     &reqID,
		MediaType:     "movie",
		TmdbID:        nz(movie.TmdbID),
		RadarrMovieID: &radarrID,
		Title:         movie.Title,
	})
	if err != nil {
		p.Log.Error("resolve: ensure movie item", "err", err)
		return nil, reqID
	}
	item, err := p.Store.GetMediaItem(ctx, itemID)
	if err != nil || item == nil {
		return nil, reqID
	}
	return item, reqID
}

func (p *Projector) applyGrab(ctx context.Context, eventID int64, op GrabOp) (string, int64, int64, int64) {
	var items []*store.MediaItem
	var reqID int64
	if op.Arr == "sonarr" {
		m, r := p.resolveEpisodeItems(ctx, op.Series, op.Episodes)
		reqID = r
		for _, it := range m {
			items = append(items, it)
		}
	} else {
		it, r := p.resolveMovieItem(ctx, op.Movie)
		reqID = r
		if it != nil {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return "orphan", reqID, 0, 0
	}

	dlKey := store.NormalizeDownloadID(op.DownloadID)
	var dlID int64
	if dlKey != "" {
		var err error
		dlID, err = p.Store.UpsertDownload(ctx, store.Download{
			ClientDownloadID: dlKey,
			Arr:              op.Arr,
			Source:           op.Protocol,
			ReleaseTitle:     op.ReleaseTitle,
			Indexer:          op.Indexer,
			SizeBytes:        nz(op.Size),
		})
		if err != nil {
			p.Log.Error("grab: upsert download", "err", err)
			dlID = 0
		}
	}

	var firstItem int64
	for _, item := range items {
		cycle := item.CurrentCycle
		if dlKey != "" {
			// A download already linked to this item (any cycle) means this
			// grab is a replay (webhook + history poller) — apply it to the
			// cycle it belongs to; a late replay of an old download must
			// never bump the current cycle.
			if linked, _ := p.Store.CycleForItemDownload(ctx, item.ID, dlKey); linked > 0 {
				cycle = linked
			} else if has, _ := p.Store.HasTransition(ctx, item.ID, cycle, "grabbed"); has {
				// Same cycle already grabbed from a different download →
				// genuine retry/upgrade, start a fresh cycle.
				prev, _ := p.Store.DownloadClientIDForItemCycle(ctx, item.ID, cycle)
				if prev != "" && prev != dlKey {
					if next, err := p.Store.BumpItemCycle(ctx, item.ID); err == nil {
						cycle = next
					}
				}
			}
		}
		if dlID != 0 {
			_ = p.Store.LinkDownloadItem(ctx, dlID, item.ID, cycle)
		}
		applied, err := p.Store.ApplyStage(ctx, item.ID, cycle, "grabbed", eventID, op.ReleaseTitle)
		if err != nil {
			p.Log.Error("grab: apply stage", "item", item.ID, "err", err)
			continue
		}
		if applied {
			p.publishStage(item.ID, reqID, "grabbed", cycle)
			p.touch(reqID)
		}
		if firstItem == 0 {
			firstItem = item.ID
		}
	}
	return "matched", reqID, firstItem, dlID
}

func (p *Projector) applyImport(ctx context.Context, eventID int64, op ImportOp) (string, int64, int64, int64) {
	dlKey := store.NormalizeDownloadID(op.DownloadID)

	// importOne applies 'imported' to the cycle the download is linked to —
	// a late import of a superseded download lands in its own (old) cycle
	// and cannot advance the item's current pointer.
	importOne := func(item *store.MediaItem, reqID int64, path string) {
		cycle := item.CurrentCycle
		if dlKey != "" {
			if linked, _ := p.Store.CycleForItemDownload(ctx, item.ID, dlKey); linked > 0 {
				cycle = linked
			}
		}
		applied, err := p.Store.ApplyStage(ctx, item.ID, cycle, "imported", eventID, "")
		if err != nil {
			p.Log.Error("import: apply stage", "item", item.ID, "err", err)
			return
		}
		if applied {
			p.publishStage(item.ID, reqID, "imported", cycle)
			p.touch(reqID)
		}
		if path != "" && cycle == item.CurrentCycle {
			_ = p.Store.SetItemImportedPath(ctx, item.ID, path)
		}
	}

	var reqID, firstItem int64
	if op.Arr == "sonarr" {
		m, r := p.resolveEpisodeItems(ctx, op.Series, op.Episodes)
		reqID = r
		for epID, item := range m {
			importOne(item, reqID, op.EpisodePaths[epID])
			if firstItem == 0 {
				firstItem = item.ID
			}
		}
	} else {
		item, r := p.resolveMovieItem(ctx, op.Movie)
		reqID = r
		if item != nil {
			importOne(item, reqID, op.MoviePath)
			firstItem = item.ID
		}
	}
	if firstItem == 0 {
		return "orphan", reqID, 0, 0
	}

	var dlID int64
	if dlKey != "" {
		if dl, err := p.Store.FindDownloadByClientID(ctx, dlKey); err == nil && dl != nil {
			dlID = dl.ID
			remaining, err := p.Store.UnimportedItemCount(ctx, dl.ID)
			if err == nil && remaining == 0 && dl.State != "failed" && dl.State != "canceled" {
				_ = p.Store.SetDownloadState(ctx, dl.ID, "imported", "")
			}
		}
	}
	return "matched", reqID, firstItem, dlID
}

func (p *Projector) applyFailure(ctx context.Context, eventID int64, op FailureOp) (string, int64, int64, int64) {
	msg := op.Message
	if msg == "" {
		msg = "download failed"
	}
	var reqID, firstItem, dlID int64
	key := store.NormalizeDownloadID(op.DownloadID)

	// markItem applies the error only when this failure belongs to the
	// item's CURRENT attempt — a stale failure of a superseded download must
	// not smear an error over a cycle that already moved on.
	markItem := func(item *store.MediaItem) {
		if key != "" {
			linked, _ := p.Store.CycleForItemDownload(ctx, item.ID, key)
			switch {
			case linked > 0 && linked != item.CurrentCycle:
				return // failure of an old attempt
			case linked == 0:
				// download not linked to this item: only mark when the
				// current cycle isn't already claimed by another download
				prev, _ := p.Store.DownloadClientIDForItemCycle(ctx, item.ID, item.CurrentCycle)
				if prev != "" && prev != key {
					return
				}
			}
		}
		_ = p.Store.SetItemError(ctx, item.ID, msg)
	}

	if key != "" {
		if dl, err := p.Store.FindDownloadByClientID(ctx, key); err == nil && dl != nil {
			dlID = dl.ID
			if !op.Soft && dl.State != "imported" && dl.State != "canceled" {
				_ = p.Store.SetDownloadState(ctx, dl.ID, "failed", msg)
			}
			itemIDs, _ := p.Store.ItemIDsForDownload(ctx, dl.ID)
			for _, id := range itemIDs {
				item, err := p.Store.GetMediaItem(ctx, id)
				if err != nil || item == nil {
					continue
				}
				markItem(item)
				if item.RequestID != nil {
					reqID = *item.RequestID
					p.touch(reqID)
				}
				if firstItem == 0 {
					firstItem = id
				}
			}
		}
	}
	// Fallback: resolve by episode/movie refs when the download row is
	// unknown or had no linked items.
	if firstItem == 0 {
		if op.Arr == "sonarr" && op.Series != nil {
			m, r := p.resolveEpisodeItems(ctx, op.Series, op.Episodes)
			reqID = r
			for _, item := range m {
				markItem(item)
				p.touch(reqID)
				if firstItem == 0 {
					firstItem = item.ID
				}
			}
		} else if op.Movie != nil {
			item, r := p.resolveMovieItem(ctx, op.Movie)
			reqID = r
			if item != nil {
				markItem(item)
				p.touch(reqID)
				firstItem = item.ID
			}
		}
	}
	if firstItem == 0 {
		return "orphan", 0, 0, dlID
	}
	return "matched", reqID, firstItem, dlID
}
