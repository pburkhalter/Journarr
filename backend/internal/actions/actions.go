// Package actions performs the v1 interventions the UI exposes: retry a
// failed/stuck download, cancel a request end-to-end, trigger a Jellyfin
// library scan. Each records an audit row and publishes an action.result.
package actions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

type Actions struct {
	Store   *store.Store
	Log     *slog.Logger
	Sonarr  *clients.Arr
	Radarr  *clients.Arr
	Seerr   *clients.Seerr
	Jelly   *clients.Jellyfin
	Wake    func()
	Publish func(event string, data any)
}

func isTerminal(state string) bool {
	return state == "imported" || state == "failed" || state == "canceled"
}

func (a *Actions) arrFor(mediaType, arr string) *clients.Arr {
	if arr == "radarr" || mediaType == "movie" {
		return a.Radarr
	}
	return a.Sonarr
}

func (a *Actions) finish(ctx context.Context, id int64, err error) error {
	status, detail := "ok", ""
	if err != nil {
		status, detail = "failed", err.Error()
	}
	_ = a.Store.FinishAction(ctx, id, status, detail)
	if a.Publish != nil {
		a.Publish("action.result", map[string]any{"id": id, "status": status, "detail": detail})
	}
	return err
}

// JellyfinScan triggers a full library refresh.
func (a *Actions) JellyfinScan(ctx context.Context) error {
	id, _ := a.Store.InsertAction(ctx, "jellyfin_scan", "", 0)
	if a.Jelly == nil {
		return a.finish(ctx, id, fmt.Errorf("jellyfin not configured"))
	}
	return a.finish(ctx, id, a.Jelly.RefreshLibrary(ctx))
}

// Retry cancels the item's in-flight download(s) (blocklisting the bad
// release) and re-searches. A season pack is ONE download linked to many
// episodes, so retrying one episode re-searches every sibling that share the
// download — otherwise they would be orphaned (queue removed + blocklisted but
// never re-grabbed). Each affected item starts a fresh cycle.
func (a *Actions) Retry(ctx context.Context, mediaItemID int64) error {
	id, _ := a.Store.InsertAction(ctx, "retry", "media_item", mediaItemID)
	item, err := a.Store.GetMediaItem(ctx, mediaItemID)
	if err != nil || item == nil {
		return a.finish(ctx, id, fmt.Errorf("media item %d not found", mediaItemID))
	}
	arr := a.arrFor(item.MediaType, "")
	if arr == nil {
		return a.finish(ctx, id, fmt.Errorf("arr not configured for %s", item.MediaType))
	}

	// Collect the clicked item + every sibling sharing its in-flight download.
	affected := map[int64]*store.MediaItem{mediaItemID: item}
	downloads, _ := a.Store.DownloadsForItemCycle(ctx, mediaItemID, item.CurrentCycle)
	for _, dl := range downloads {
		if isTerminal(dl.State) {
			continue // already imported/failed/canceled — leave it alone
		}
		if dl.ClientDownloadID != "" {
			if qids, err := arr.QueueRecordIDsForDownload(ctx, dl.ClientDownloadID); err == nil {
				for _, qid := range qids {
					if e := arr.DeleteQueueItem(ctx, qid, true, true); e != nil {
						a.Log.Warn("retry: delete queue item", "qid", qid, "err", e)
					}
				}
			}
		}
		_ = a.Store.SetDownloadState(ctx, dl.ID, "canceled", "superseded by retry")
		ids, _ := a.Store.ItemIDsForDownload(ctx, dl.ID)
		for _, lid := range ids {
			if _, ok := affected[lid]; ok {
				continue
			}
			if it, err := a.Store.GetMediaItem(ctx, lid); err == nil && it != nil {
				affected[lid] = it
			}
		}
	}

	// Re-search the whole affected set (homogeneous — one download is one
	// arr) and bump each cycle so the incoming grab starts clean.
	var episodeIDs, movieIDs []int64
	for _, it := range affected {
		if it.MediaType == "movie" && it.RadarrMovieID != nil {
			movieIDs = append(movieIDs, *it.RadarrMovieID)
		} else if it.SonarrEpisodeID != nil {
			episodeIDs = append(episodeIDs, *it.SonarrEpisodeID)
		}
		_, _ = a.Store.BumpItemCycle(ctx, it.ID)
	}

	var searchErr error
	switch {
	case len(movieIDs) > 0:
		searchErr = arr.Command(ctx, "MoviesSearch", map[string]any{"movieIds": movieIDs})
	case len(episodeIDs) > 0:
		searchErr = arr.Command(ctx, "EpisodeSearch", map[string]any{"episodeIds": episodeIDs})
	default:
		searchErr = fmt.Errorf("no arr id on item %d to search", mediaItemID)
	}
	if searchErr != nil {
		return a.finish(ctx, id, searchErr)
	}
	if a.Wake != nil {
		a.Wake()
	}
	return a.finish(ctx, id, nil)
}

// Cancel stops a request end-to-end: cancel in-flight downloads (no blocklist),
// UNMONITOR the episodes/movies in the arr so it stops re-downloading, remove
// the Seerr request, mark canceled and clear any stuck flags.
func (a *Actions) Cancel(ctx context.Context, requestID int64) error {
	id, _ := a.Store.InsertAction(ctx, "cancel", "request", requestID)
	req, err := a.Store.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return a.finish(ctx, id, fmt.Errorf("request %d not found", requestID))
	}

	downloads, _ := a.Store.DownloadsForRequest(ctx, requestID)
	for _, dl := range downloads {
		if isTerminal(dl.State) {
			continue
		}
		arr := a.arrFor("", dl.Arr)
		if arr != nil && dl.ClientDownloadID != "" {
			if qids, err := arr.QueueRecordIDsForDownload(ctx, dl.ClientDownloadID); err == nil {
				for _, qid := range qids {
					if e := arr.DeleteQueueItem(ctx, qid, true, false); e != nil {
						a.Log.Warn("cancel: delete queue item", "qid", qid, "err", e)
					}
				}
			}
		}
		_ = a.Store.SetDownloadState(ctx, dl.ID, "canceled", "canceled by user")
	}

	// Unmonitor in the arr so it doesn't immediately re-grab (the queue delete
	// used blocklist=false, so the release isn't banned — only unmonitoring
	// stops it).
	items, _ := a.Store.ListItemsForRequest(ctx, requestID)
	var episodeIDs []int64
	for _, it := range items {
		if it.MediaType == "movie" && it.RadarrMovieID != nil && a.Radarr != nil {
			if e := a.Radarr.SetMovieMonitored(ctx, *it.RadarrMovieID, false); e != nil {
				a.Log.Warn("cancel: unmonitor movie", "movie", *it.RadarrMovieID, "err", e)
			}
		} else if it.SonarrEpisodeID != nil {
			episodeIDs = append(episodeIDs, *it.SonarrEpisodeID)
		}
	}
	if len(episodeIDs) > 0 && a.Sonarr != nil {
		if e := a.Sonarr.SetEpisodesMonitored(ctx, episodeIDs, false); e != nil {
			a.Log.Warn("cancel: unmonitor episodes", "count", len(episodeIDs), "err", e)
		}
	}

	if req.SeerrRequestID != nil && a.Seerr != nil {
		if err := a.Seerr.DeleteRequest(ctx, *req.SeerrRequestID); err != nil {
			a.Log.Warn("cancel: seerr delete", "seerr_id", *req.SeerrRequestID, "err", err)
		}
	}
	if err := a.Store.SetRequestStatus(ctx, requestID, "canceled"); err != nil {
		return a.finish(ctx, id, err)
	}
	_ = a.Store.ClearStuckForRequest(ctx, requestID)
	if a.Publish != nil {
		a.Publish("request.updated", map[string]any{"id": requestID, "status": "canceled"})
	}
	return a.finish(ctx, id, nil)
}
