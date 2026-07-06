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

// Retry cancels the item's current download (blocklisting the bad release) and
// re-searches, starting a fresh cycle.
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

	// Cancel + blocklist the current download(s) so a re-search avoids them.
	if err := a.cancelItemDownloads(ctx, mediaItemID, true); err != nil {
		a.Log.Warn("retry: cancel downloads", "item", mediaItemID, "err", err)
	}

	// Trigger a fresh search.
	var searchErr error
	if item.MediaType == "movie" && item.RadarrMovieID != nil {
		searchErr = arr.Command(ctx, "MoviesSearch", map[string]any{"movieIds": []int64{*item.RadarrMovieID}})
	} else if item.SonarrEpisodeID != nil {
		searchErr = arr.Command(ctx, "EpisodeSearch", map[string]any{"episodeIds": []int64{*item.SonarrEpisodeID}})
	} else {
		searchErr = fmt.Errorf("no arr id on item %d to search", mediaItemID)
	}
	if searchErr != nil {
		return a.finish(ctx, id, searchErr)
	}

	// New attempt: bump the cycle so the incoming grab starts clean.
	if _, err := a.Store.BumpItemCycle(ctx, mediaItemID); err != nil {
		a.Log.Warn("retry: bump cycle", "item", mediaItemID, "err", err)
	}
	if a.Wake != nil {
		a.Wake()
	}
	return a.finish(ctx, id, nil)
}

// Cancel stops a request end-to-end: cancel in-flight downloads (no blocklist),
// decline/remove the Seerr request, mark it canceled. The series/movie itself
// is left in the arr (v1).
func (a *Actions) Cancel(ctx context.Context, requestID int64) error {
	id, _ := a.Store.InsertAction(ctx, "cancel", "request", requestID)
	req, err := a.Store.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return a.finish(ctx, id, fmt.Errorf("request %d not found", requestID))
	}

	downloads, _ := a.Store.DownloadsForRequest(ctx, requestID)
	for _, dl := range downloads {
		if dl.State == "imported" || dl.State == "failed" || dl.State == "canceled" {
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

	if req.SeerrRequestID != nil && a.Seerr != nil {
		if err := a.Seerr.DeleteRequest(ctx, *req.SeerrRequestID); err != nil {
			a.Log.Warn("cancel: seerr delete", "seerr_id", *req.SeerrRequestID, "err", err)
		}
	}
	if err := a.Store.SetRequestStatus(ctx, requestID, "canceled"); err != nil {
		return a.finish(ctx, id, err)
	}
	if a.Publish != nil {
		a.Publish("request.updated", map[string]any{"id": requestID, "status": "canceled"})
	}
	return a.finish(ctx, id, nil)
}

// cancelItemDownloads deletes the arr queue records for the downloads linked to
// an item (optionally blocklisting) and marks them canceled.
func (a *Actions) cancelItemDownloads(ctx context.Context, mediaItemID int64, blocklist bool) error {
	item, err := a.Store.GetMediaItem(ctx, mediaItemID)
	if err != nil || item == nil {
		return err
	}
	arr := a.arrFor(item.MediaType, "")
	dls, err := a.Store.DownloadsForItemCycle(ctx, mediaItemID, item.CurrentCycle)
	if err != nil {
		return err
	}
	for _, dl := range dls {
		if arr != nil && dl.ClientDownloadID != "" {
			if qids, err := arr.QueueRecordIDsForDownload(ctx, dl.ClientDownloadID); err == nil {
				for _, qid := range qids {
					_ = arr.DeleteQueueItem(ctx, qid, true, blocklist)
				}
			}
		}
		_ = a.Store.SetDownloadState(ctx, dl.ID, "canceled", "superseded by retry")
	}
	return nil
}
