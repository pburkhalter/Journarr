package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/store"
)

// ArrHistoryPoller is the authoritative reconciler: grabs and imports that
// webhooks missed, plus downloadFailed — which has NO webhook equivalent.
type ArrHistoryPoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Arr      *clients.Arr // Name = sonarr|radarr
	Interval time.Duration
	Wake     func()
}

func (p *ArrHistoryPoller) cursorKey() string { return p.Arr.Name + ":history" }

func (p *ArrHistoryPoller) Run(ctx context.Context) {
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

func (p *ArrHistoryPoller) pass(ctx context.Context) {
	cur, err := p.Store.GetPollCursor(ctx, p.cursorKey())
	if err != nil {
		p.Log.Warn("history poll: cursor", "err", err)
		return
	}
	sinceID, _ := strconv.ParseInt(cur, 10, 64)
	firstRun := sinceID == 0

	records, complete, err := p.Arr.HistorySince(ctx, sinceID, 10)
	if err != nil {
		p.Log.Warn("history poll: fetch", "arr", p.Arr.Name, "err", err)
		return
	}
	if len(records) == 0 {
		return
	}
	if !complete {
		// More than the page budget accumulated since the cursor (long
		// downtime + import burst). The window below still processes ~1000
		// records; anything older is lost to history — say so loudly.
		p.Log.Error("history poll: window truncated, oldest records skipped",
			"arr", p.Arr.Name, "since", sinceID, "fetched", len(records))
	}

	// Records are oldest-first. The cursor only ever advances over records
	// that were durably inserted — an InsertEvent error (locked DB, full
	// disk) aborts the pass and the next pass resumes exactly there. This
	// matters most for downloadFailed, which has no webhook equivalent.
	cursor := sinceID
	inserted := 0
	for _, rec := range records {
		// First run: only advance the cursor — no backfill flood into a
		// fresh install (backfill becomes an explicit M4 command).
		if !firstRun {
			kind, op := p.normalize(rec)
			if kind != "" {
				payload, _ := json.Marshal(op)
				dedupe := fmt.Sprintf("%s:history:%d", p.Arr.Name, rec.ID)
				if _, ok, err := p.Store.InsertEvent(ctx, p.Arr.Name, kind, dedupe, payload); err != nil {
					p.Log.Warn("history poll: insert failed, pass aborted", "err", err)
					break
				} else if ok {
					inserted++
				}
			}
		}
		if rec.ID > cursor {
			cursor = rec.ID
		}
	}
	if cursor != sinceID {
		if err := p.Store.SetPollCursor(ctx, p.cursorKey(), strconv.FormatInt(cursor, 10)); err != nil {
			p.Log.Warn("history poll: save cursor", "err", err)
		}
	}
	if inserted > 0 {
		p.Log.Info("history poll: new records", "arr", p.Arr.Name, "count", inserted)
		if p.Wake != nil {
			p.Wake()
		}
	}
}

func (p *ArrHistoryPoller) normalize(rec clients.HistoryRecord) (string, any) {
	var series *pipeline.SeriesRef
	var episodes []pipeline.EpisodeRef
	var movie *pipeline.MovieRef
	if rec.Series != nil {
		series = &pipeline.SeriesRef{
			SonarrID: rec.Series.ID, TvdbID: rec.Series.TvdbID, Title: rec.Series.Title,
		}
	}
	if rec.Episode != nil {
		episodes = []pipeline.EpisodeRef{{
			SonarrID: rec.Episode.ID, Season: rec.Episode.SeasonNumber,
			Episode: rec.Episode.EpisodeNumber, Title: rec.Episode.Title,
		}}
	}
	if rec.Movie != nil {
		movie = &pipeline.MovieRef{RadarrID: rec.Movie.ID, TmdbID: rec.Movie.TmdbID, Title: rec.Movie.Title, Year: rec.Movie.Year}
	}

	switch rec.EventType {
	case "grabbed":
		size, _ := strconv.ParseInt(rec.Data["size"], 10, 64)
		return "grab", pipeline.GrabOp{
			Arr: p.Arr.Name, DownloadID: rec.DownloadID,
			ReleaseTitle: rec.SourceTitle,
			Indexer:      rec.Data["indexer"],
			Size:         size,
			Protocol:     rec.Data["protocol"],
			Series:       series, Episodes: episodes, Movie: movie,
		}
	case "downloadFolderImported", "movieFolderImported":
		op := pipeline.ImportOp{
			Arr: p.Arr.Name, DownloadID: rec.DownloadID,
			Series: series, Episodes: episodes, Movie: movie,
		}
		if path := rec.Data["importedPath"]; path != "" {
			if rec.Episode != nil {
				op.EpisodePaths = map[int64]string{rec.Episode.ID: path}
			} else {
				op.MoviePath = path
			}
		}
		return "import", op
	case "downloadFailed":
		msg := rec.Data["message"]
		if msg == "" {
			msg = "download failed"
		}
		return "failure", pipeline.FailureOp{
			Arr: p.Arr.Name, DownloadID: rec.DownloadID, Message: msg,
			Series: series, Episodes: episodes, Movie: movie,
		}
	}
	return "", nil
}

var _ = time.Second
