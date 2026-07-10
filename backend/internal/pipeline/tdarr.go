package pipeline

import (
	"context"

	"github.com/pburkhalter/journarr/internal/store"
)

// applyTranscode marks a media item as passing through the Tdarr transcode
// stage (ordinal 80, between imported 70 and available 90). It's an optional
// waypoint: if Tdarr never touches a file, Jellyfin's 'available' (90) still
// advances past it, and a late transcode event can't regress a further stage
// (forward-only by ordinal), it just lands in the history.
func (p *Projector) applyTranscode(ctx context.Context, eventID int64, op TranscodeOp) (string, int64, int64, int64) {
	item := p.resolveTranscodeItem(ctx, op)
	if item == nil {
		return "orphan", 0, 0, 0
	}
	var reqID int64
	if item.RequestID != nil {
		reqID = *item.RequestID
	}
	note := "tdarr"
	if op.Phase == "complete" {
		note = "tdarr complete"
	}
	p.apply(ctx, item.ID, reqID, eventID, "transcode", note)
	return "matched", reqID, item.ID, 0
}

// resolveTranscodeItem matches a transcode event to a media item. Explicit ids
// win when present; otherwise the file path (which is all Tdarr reliably knows)
// resolves via imported_path — exact, then basename.
func (p *Projector) resolveTranscodeItem(ctx context.Context, op TranscodeOp) *store.MediaItem {
	if op.TvdbID > 0 && op.Episode > 0 {
		if mi, _ := p.Store.FindEpisodeItemByTvdb(ctx, op.TvdbID, op.Season, op.Episode); mi != nil {
			return mi
		}
	}
	if op.TmdbID > 0 {
		if mi, _ := p.Store.FindMovieItemByTmdb(ctx, op.TmdbID); mi != nil {
			return mi
		}
	}
	if op.File != "" {
		if mi, _ := p.Store.FindItemByImportedPath(ctx, op.File); mi != nil {
			return mi
		}
	}
	return nil
}
