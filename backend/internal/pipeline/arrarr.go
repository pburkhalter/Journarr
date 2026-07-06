package pipeline

import (
	"context"

	"github.com/pburkhalter/journarr/internal/store"
)

// arrarrStageMap maps Arrarr job states onto Journarr download states and
// pipeline stages (the TorBox substages between grabbed and imported).
var arrarrStageMap = map[string]struct {
	dlState string
	stage   string
}{
	"SUBMITTED":        {"submitted", "submitted"},
	"DOWNLOADING":      {"cloud_downloading", "cloud_downloading"},
	"COMPLETED_TORBOX": {"pulling", "pulling"},
	"READY":            {"downloaded", "downloaded"},
}

// downloadStateRank orders the forward progression of a download so an
// out-of-order arrarr event (e.g. a late DOWNLOADING after COMPLETED_TORBOX)
// cannot move the download state backwards. Terminal states rank -1.
var downloadStateRank = map[string]int{
	"grabbed": 0, "submitted": 1, "cloud_downloading": 2,
	"pulling": 3, "downloaded": 4, "imported": 5,
	"failed": -1, "canceled": -1,
}

// applyJobTransition advances the download and its linked items to the
// substage matching the Arrarr job state. FAILED/CANCELED mark the download
// failed/canceled and annotate items in the download's cycle.
func (p *Projector) applyJobTransition(ctx context.Context, eventID int64, op JobTransitionOp) (string, int64, int64, int64) {
	dl := p.findArrarrDownload(ctx, op)
	if dl == nil {
		// No matching grab yet. In the normal flow the arr grab (which
		// creates the download) reaches Journarr before Arrarr's first
		// transition, so this only happens under webhook reordering. The
		// substage for THIS event is dropped, but the item stage is
		// forward-only and catches up on the next transition once the grab
		// has landed (e.g. straight to cloud_downloading) — no permanent
		// loss beyond a missing early history row.
		return "orphan", 0, 0, 0
	}

	// Remember the arrarr nzo id for the reconciler and actions.
	if op.NzoID != "" && dl.ArrarrNzoID == "" {
		_ = p.Store.SetDownloadArrarrNzo(ctx, dl.ID, op.NzoID)
	}

	switch op.To {
	case "FAILED", "CANCELED":
		state := "failed"
		if op.To == "CANCELED" {
			state = "canceled"
		}
		if dl.State != "imported" {
			_ = p.Store.SetDownloadState(ctx, dl.ID, state, op.LastError)
		}
		reqID, first := p.annotateDownloadItems(ctx, dl.ID, op.LastError)
		return "matched", reqID, first, dl.ID
	}

	m, ok := arrarrStageMap[op.To]
	if !ok {
		return "ignored", 0, 0, dl.ID
	}
	// Only advance the download state — never regress it (out-of-order or
	// duplicate arrarr events, or a state that already reached import).
	if downloadStateRank[dl.State] >= 0 && downloadStateRank[m.dlState] > downloadStateRank[dl.State] {
		_ = p.Store.SetDownloadState(ctx, dl.ID, m.dlState, "")
	}
	if op.BytesTotal > 0 {
		_ = p.Store.UpdateDownloadProgress(ctx, dl.ID, op.BytesDownloaded, op.BytesTotal)
	}

	itemIDs, _ := p.Store.ItemIDsForDownload(ctx, dl.ID)
	var reqID, first int64
	for _, id := range itemIDs {
		item, err := p.Store.GetMediaItem(ctx, id)
		if err != nil || item == nil {
			continue
		}
		// Apply to the cycle this download belongs to.
		cycle := item.CurrentCycle
		if linked, _ := p.Store.CycleForItemDownload(ctx, id, dl.ClientDownloadID); linked > 0 {
			cycle = linked
		}
		applied, err := p.Store.ApplyStage(ctx, id, cycle, m.stage, eventID, "")
		if err != nil {
			p.Log.Error("arrarr: apply stage", "item", id, "stage", m.stage, "err", err)
			continue
		}
		if applied {
			if item.RequestID != nil {
				reqID = *item.RequestID
				p.publishStage(id, reqID, m.stage, cycle)
				p.touch(reqID)
			}
		}
		if first == 0 {
			first = id
		}
	}
	if op.LocalPath != "" {
		p.Publish("download.progress", map[string]any{
			"download_id": dl.ID, "bytes_downloaded": op.BytesDownloaded, "bytes_total": op.BytesTotal,
		})
	}
	return "matched", reqID, first, dl.ID
}

// findArrarrDownload matches an arrarr job to a Journarr download by nzo_id or
// by the torrent infohash (both stored lowercase in client_download_id).
func (p *Projector) findArrarrDownload(ctx context.Context, op JobTransitionOp) *store.Download {
	for _, key := range []string{op.NzoID, op.NzbSHA256} {
		if key == "" {
			continue
		}
		if dl, err := p.Store.FindDownloadByClientID(ctx, key); err == nil && dl != nil {
			return dl
		}
	}
	// Also try the resolved arrarr_nzo_id column.
	if op.NzoID != "" {
		if dl, err := p.Store.FindDownloadByArrarrNzo(ctx, op.NzoID); err == nil && dl != nil {
			return dl
		}
	}
	return nil
}

// applyAvailable marks a media item available in Jellyfin (the poller already
// resolved the match).
func (p *Projector) applyAvailable(ctx context.Context, eventID int64, op AvailableOp) (string, int64, int64, int64) {
	if op.MediaItemID == 0 {
		return "ignored", 0, 0, 0
	}
	item, err := p.Store.GetMediaItem(ctx, op.MediaItemID)
	if err != nil || item == nil {
		return "ignored", 0, 0, 0
	}
	if op.JellyfinItemID != "" && item.JellyfinItemID == "" {
		_ = p.Store.SetItemJellyfinID(ctx, item.ID, op.JellyfinItemID)
	}
	var reqID int64
	if item.RequestID != nil {
		reqID = *item.RequestID
	}
	note := op.Note
	if note == "" {
		note = "jellyfin"
	}
	p.apply(ctx, item.ID, reqID, eventID, "available", note)
	return "matched", reqID, item.ID, 0
}

// applyNotified marks the item(s) the concierge just messaged about as
// 'notified'. Concierge notifies at import time, so this often precedes the
// Jellyfin 'available' event — the forward-only stage logic handles that, and
// 'notified' (the last stage) also serves as a completion safety net when
// Jellyfin matching never lands.
func (p *Projector) applyNotified(ctx context.Context, eventID int64, op NotifiedOp) (string, int64, int64, int64) {
	if op.TmdbID == 0 {
		return "ignored", 0, 0, 0
	}
	if op.MediaType == "movie" {
		mi, err := p.Store.FindMovieItemByTmdb(ctx, op.TmdbID)
		if err != nil || mi == nil {
			return "orphan", 0, 0, 0
		}
		var reqID int64
		if mi.RequestID != nil {
			reqID = *mi.RequestID
		}
		p.apply(ctx, mi.ID, reqID, eventID, "notified", "waha")
		return "matched", reqID, mi.ID, 0
	}

	// tv: bridge series tmdb -> request -> episodes by season/episode.
	req, err := p.Store.FindRequestByTmdb(ctx, op.TmdbID, "tv")
	if err != nil || req == nil {
		return "orphan", 0, 0, 0
	}
	var first int64
	for _, e := range op.Episodes {
		mi, err := p.Store.FindItemByEpisodeNumbers(ctx, req.ID, e.Season, e.Episode)
		if err != nil || mi == nil {
			continue
		}
		p.apply(ctx, mi.ID, req.ID, eventID, "notified", "waha")
		if first == 0 {
			first = mi.ID
		}
	}
	if first == 0 {
		return "orphan", req.ID, 0, 0
	}
	return "matched", req.ID, first, 0
}

func (p *Projector) annotateDownloadItems(ctx context.Context, downloadID int64, msg string) (int64, int64) {
	if msg == "" {
		msg = "arrarr: job failed"
	}
	itemIDs, _ := p.Store.ItemIDsForDownload(ctx, downloadID)
	var reqID, first int64
	for _, id := range itemIDs {
		item, err := p.Store.GetMediaItem(ctx, id)
		if err != nil || item == nil {
			continue
		}
		if linked, _ := p.Store.CycleForItemDownloadByID(ctx, id, downloadID); linked == item.CurrentCycle {
			_ = p.Store.SetItemError(ctx, id, msg)
		}
		if item.RequestID != nil {
			reqID = *item.RequestID
			p.touch(reqID)
		}
		if first == 0 {
			first = id
		}
	}
	return reqID, first
}
