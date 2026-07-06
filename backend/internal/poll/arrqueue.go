package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// ArrQueuePoller feeds live progress bars. Byte counters are ephemeral state,
// not transitions — they bypass the events table and write directly.
type ArrQueuePoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Arrs     []*clients.Arr
	Interval time.Duration
	Publish  func(event string, data any)
}

func (p *ArrQueuePoller) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pass(ctx)
		}
	}
}

func (p *ArrQueuePoller) pass(ctx context.Context) {
	active, err := p.Store.ActiveDownloads(ctx)
	if err != nil {
		p.Log.Warn("queue poll: active downloads", "err", err)
		return
	}
	if len(active) == 0 {
		return
	}
	byClientID := map[string]*store.Download{}
	for i := range active {
		byClientID[active[i].ClientDownloadID] = &active[i]
	}

	for _, arr := range p.Arrs {
		records, err := arr.Queue(ctx)
		if err != nil {
			p.Log.Warn("queue poll: fetch", "arr", arr.Name, "err", err)
			continue
		}
		seen := map[string]bool{}
		for _, rec := range records {
			key := store.NormalizeDownloadID(rec.DownloadID)
			dl, ok := byClientID[key]
			if !ok || seen[key] {
				continue // season packs: one row per episode, same totals
			}
			seen[key] = true
			total := int64(rec.Size)
			downloaded := total - int64(rec.SizeLeft)
			if downloaded < 0 {
				downloaded = 0
			}
			if err := p.Store.UpdateDownloadProgress(ctx, dl.ID, downloaded, total); err != nil {
				continue
			}
			if p.Publish != nil {
				p.Publish("download.progress", map[string]any{
					"download_id": dl.ID, "bytes_downloaded": downloaded, "bytes_total": total,
					"tracked_state": rec.TrackedDownloadState,
				})
			}
			// Surface import blockages on the linked items.
			if rec.TrackedDownloadStatus == "warning" || rec.TrackedDownloadStatus == "error" {
				if rec.ErrorMessage != "" {
					itemIDs, _ := p.Store.ItemIDsForDownload(ctx, dl.ID)
					for _, id := range itemIDs {
						_ = p.Store.SetItemError(ctx, id, rec.ErrorMessage)
					}
				}
			}
		}
	}
}

var _ = time.Second
