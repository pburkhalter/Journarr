package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// "Remove everywhere": a series or movie goes from every system, whatever
// state it is in. The cascade uses each system's own delete, so the download
// client is reached through Sonarr/Radarr (queue delete with
// removeFromClient → SABnzbd delete with del_files=1), never directly:
//
//  1. refuse while someone watches it in Jellyfin (unless forced)
//  2. Sonarr/Radarr: remove its queue entries (stops downloads, deletes them)
//  3. Sonarr/Radarr: delete the series/movie with its files
//  4. Seerr: delete the media entry with its requests
//  5. Jellyfin: check it left the library, else trigger a scan
//  6. Journarr: purge its requests and leave a tombstone
//
// Steps 2 and 3 stop the cascade on failure — Seerr and Journarr then still
// show the title, and a second click resumes (every step treats "already
// gone" as done). 4 and 5 are best effort.

// ErrBeingWatched means someone is playing the title right now.
var ErrBeingWatched = errors.New("someone is watching this title")

// removeSettle is how long to give Sonarr/Radarr's Jellyfin notification to
// drop the title before checking. Tests set it to 0.
var removeSettle = 5 * time.Second

// RemovalPlan describes what a removal would delete (the confirmation
// dialog) and carries the ids Remove needs.
type RemovalPlan struct {
	RequestID  int64    `json:"request_id"`
	Title      string   `json:"title"`
	MediaType  string   `json:"media_type"`
	Requests   int      `json:"requests"` // Journarr requests for this title
	Arr        string   `json:"arr,omitempty"`
	ArrID      int64    `json:"arr_id,omitempty"`
	Path       string   `json:"path,omitempty"`
	Files      int64    `json:"files"`
	SizeBytes  int64    `json:"size_bytes"`
	Seasons    int64    `json:"seasons,omitempty"`
	Downloads  []string `json:"downloads"` // queue entries that will be stopped
	InSeerr    bool     `json:"in_seerr"`
	Viewers    []string `json:"viewers"`
	Warnings   []string `json:"warnings"`
	requestIDs []int64
	seerrIDs   []int64
	seerrMedia int64
	arrIDs     []int64 // every Sonarr series / Radarr movie id seen on the items
	queueIDs   []int64
	tmdbID     int64
}

// RemovalStep is one line of the result.
type RemovalStep struct {
	Step   string `json:"step"`
	Status string `json:"status"` // ok|skipped|warning|failed
	Detail string `json:"detail,omitempty"`
}

// PlanRemoval resolves the title of a request across all systems without
// changing anything.
func (a *Actions) PlanRemoval(ctx context.Context, requestID int64) (*RemovalPlan, error) {
	req, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("request %d not found", requestID)
	}
	p := &RemovalPlan{RequestID: requestID, Title: req.Title, MediaType: req.MediaType,
		Downloads: []string{}, Viewers: []string{}, Warnings: []string{}}
	if req.TmdbID != nil {
		p.tmdbID = *req.TmdbID
	}

	reqs, err := a.Store.RequestsForTitle(ctx, req.MediaType, req.TmdbID, req.TvdbID)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		reqs = []*store.Request{req}
	}
	for _, r := range reqs {
		p.requestIDs = append(p.requestIDs, r.ID)
		if r.SeerrRequestID != nil {
			p.seerrIDs = append(p.seerrIDs, *r.SeerrRequestID)
		}
		if p.tmdbID == 0 && r.TmdbID != nil {
			p.tmdbID = *r.TmdbID
		}
	}
	p.Requests = len(reqs)
	seriesIDs, movieIDs, err := a.Store.ArrIDsForRequests(ctx, p.requestIDs)
	if err != nil {
		return nil, err
	}

	if req.MediaType == "tv" {
		p.Arr = "sonarr"
		p.arrIDs = seriesIDs
		if a.Sonarr == nil {
			p.Warnings = append(p.Warnings, "Sonarr is not configured")
		} else {
			var sr *clients.Series
			for _, r := range reqs {
				if r.TvdbID != nil && *r.TvdbID != 0 {
					if sr, err = a.Sonarr.SeriesByTvdbID(ctx, *r.TvdbID); err != nil {
						return nil, fmt.Errorf("sonarr: %w", err)
					}
					break
				}
			}
			for _, id := range seriesIDs {
				if sr != nil {
					break
				}
				if sr, err = a.Sonarr.SeriesByID(ctx, id); err != nil {
					return nil, fmt.Errorf("sonarr: %w", err)
				}
			}
			if sr != nil {
				p.ArrID, p.Path = sr.ID, sr.Path
				p.Files, p.SizeBytes, p.Seasons = sr.Statistics.EpisodeFileCount, sr.Statistics.SizeOnDisk, sr.Statistics.SeasonCount
				p.arrIDs = appendUnique(p.arrIDs, sr.ID)
			}
		}
	} else {
		p.Arr = "radarr"
		p.arrIDs = movieIDs
		if a.Radarr == nil {
			p.Warnings = append(p.Warnings, "Radarr is not configured")
		} else {
			var mv *clients.Movie
			if p.tmdbID != 0 {
				if mv, err = a.Radarr.MovieByTmdbID(ctx, p.tmdbID); err != nil {
					return nil, fmt.Errorf("radarr: %w", err)
				}
			}
			for _, id := range movieIDs {
				if mv != nil {
					break
				}
				if m, e := a.Radarr.MovieByID(ctx, id); e == nil && m != nil && m.ID != 0 {
					mv = m
				}
			}
			if mv != nil {
				p.ArrID, p.Path, p.SizeBytes = mv.ID, mv.Path, mv.SizeOnDisk
				if mv.HasFile {
					p.Files = 1
				}
				p.arrIDs = appendUnique(p.arrIDs, mv.ID)
			}
		}
	}

	if arr := a.arrFor(req.MediaType, ""); arr != nil && p.ArrID != 0 {
		queue, err := arr.Queue(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s queue: %w", p.Arr, err)
		}
		seen := map[string]bool{}
		for _, q := range queue {
			if (p.Arr == "sonarr" && q.SeriesID != p.ArrID) || (p.Arr == "radarr" && q.MovieID != p.ArrID) {
				continue
			}
			// Sonarr lists a season pack once per episode; one delete per
			// download removes it.
			key := q.DownloadID
			if key == "" {
				key = fmt.Sprint(q.ID)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			p.queueIDs = append(p.queueIDs, q.ID)
			p.Downloads = append(p.Downloads, q.Title)
		}
	}

	if a.Seerr != nil && p.tmdbID != 0 {
		if id, err := a.Seerr.MediaID(ctx, req.MediaType, p.tmdbID); err == nil {
			p.seerrMedia = id
			p.InSeerr = id != 0
		} else {
			p.Warnings = append(p.Warnings, "Seerr lookup failed: "+err.Error())
		}
	}

	if a.Jelly != nil {
		if sessions, err := a.Jelly.Sessions(ctx); err == nil {
			for _, s := range sessions {
				if watching(s, p) {
					p.Viewers = append(p.Viewers, fmt.Sprintf("%s (%s)", s.User, s.Title))
				}
			}
		} else {
			p.Warnings = append(p.Warnings, "Jellyfin sessions unavailable: "+err.Error())
		}
	}
	return p, nil
}

// watching matches a playing session to the title: by path when Jellyfin
// reports one (it sees the same /media paths as Sonarr/Radarr), else by name.
func watching(s clients.JellySession, p *RemovalPlan) bool {
	if s.Path != "" && p.Path != "" {
		return s.Path == p.Path || strings.HasPrefix(s.Path, strings.TrimRight(p.Path, "/")+"/")
	}
	if p.MediaType == "tv" {
		return s.SeriesName != "" && strings.EqualFold(s.SeriesName, p.Title)
	}
	return s.ItemName != "" && strings.EqualFold(s.ItemName, p.Title)
}

// Remove deletes the request's title everywhere; see the package comment
// above. force skips the "someone is watching" guard.
func (a *Actions) Remove(ctx context.Context, requestID int64, force bool) ([]RemovalStep, error) {
	auditID, _ := a.Store.InsertAction(ctx, "remove", "request", requestID)
	var steps []RemovalStep
	add := func(step, status, detail string) {
		steps = append(steps, RemovalStep{Step: step, Status: status, Detail: detail})
	}
	done := func(err error) ([]RemovalStep, error) {
		var parts []string
		for _, s := range steps {
			line := s.Step + ": " + s.Status
			if s.Detail != "" {
				line += " (" + s.Detail + ")"
			}
			parts = append(parts, line)
		}
		summary := strings.Join(parts, "; ")
		if err != nil && summary != "" {
			err = fmt.Errorf("%w — %s", err, summary)
		}
		return steps, a.finishDetail(ctx, auditID, err, summary)
	}

	p, err := a.PlanRemoval(ctx, requestID)
	if err != nil {
		return done(err)
	}
	if len(p.Viewers) > 0 && !force {
		return done(fmt.Errorf("%w: %s", ErrBeingWatched, strings.Join(p.Viewers, ", ")))
	}
	arr := a.arrFor(p.MediaType, "")

	// 2. Stop downloads. removeFromClient makes Sonarr/Radarr delete the job
	// in the download client with its data; no blocklist — the release was
	// fine, the title is just not wanted.
	switch {
	case arr == nil || len(p.queueIDs) == 0:
		add("downloads", "skipped", "none running")
	default:
		for _, qid := range p.queueIDs {
			if err := arr.DeleteQueueItem(ctx, qid, true, false); err != nil && !strings.Contains(err.Error(), "404") {
				add("downloads", "failed", err.Error())
				return done(fmt.Errorf("stopping downloads failed"))
			}
		}
		add("downloads", "ok", fmt.Sprintf("%d stopped", len(p.queueIDs)))
	}

	// 3. The series/movie with its files.
	switch {
	case arr == nil || p.ArrID == 0:
		add(p.Arr, "skipped", "not in "+p.Arr)
	default:
		var err error
		if p.Arr == "sonarr" {
			err = arr.DeleteSeries(ctx, p.ArrID)
		} else {
			err = arr.DeleteMovie(ctx, p.ArrID)
		}
		if err != nil {
			add(p.Arr, "failed", err.Error())
			return done(fmt.Errorf("deleting from %s failed", p.Arr))
		}
		add(p.Arr, "ok", fmt.Sprintf("%d file(s), %s", p.Files, humanBytes(p.SizeBytes)))
	}

	// 4. Seerr: the media entry takes its requests with it.
	switch {
	case a.Seerr == nil:
		add("seerr", "skipped", "not configured")
	case p.seerrMedia != 0:
		if err := a.Seerr.DeleteMedia(ctx, p.seerrMedia); err != nil {
			add("seerr", "warning", err.Error())
		} else {
			add("seerr", "ok", "")
		}
	case len(p.seerrIDs) > 0:
		var failed []string
		for _, id := range p.seerrIDs {
			if err := a.Seerr.DeleteRequest(ctx, id); err != nil {
				failed = append(failed, err.Error())
			}
		}
		if len(failed) > 0 {
			add("seerr", "warning", strings.Join(failed, "; "))
		} else {
			add("seerr", "ok", fmt.Sprintf("%d request(s)", len(p.seerrIDs)))
		}
	default:
		add("seerr", "skipped", "not in Seerr")
	}

	// 5. Jellyfin normally drops it on the Sonarr/Radarr notification.
	if a.Jelly != nil && p.Path != "" {
		if removeSettle > 0 {
			select {
			case <-time.After(removeSettle):
			case <-ctx.Done():
			}
		}
		left := 0
		if items, err := a.Jelly.ItemsByName(ctx, p.Title); err == nil {
			for _, it := range items {
				if it.Path == p.Path || strings.HasPrefix(it.Path, strings.TrimRight(p.Path, "/")+"/") {
					left++
				}
			}
		}
		if left > 0 {
			if err := a.Jelly.RefreshLibrary(ctx); err != nil {
				add("jellyfin", "warning", "still listed, scan failed: "+err.Error())
			} else {
				add("jellyfin", "ok", "library scan started")
			}
		} else {
			add("jellyfin", "ok", "")
		}
	}

	// 6. Journarr itself.
	t := store.Tombstone{Title: p.Title, SeerrRequestIDs: p.seerrIDs}
	if p.Arr == "sonarr" {
		t.SonarrSeriesIDs = p.arrIDs
	} else {
		t.RadarrMovieIDs = p.arrIDs
	}
	wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Store.PurgeRequests(wctx, p.requestIDs, t); err != nil {
		add("journarr", "failed", err.Error())
		return done(fmt.Errorf("purging Journarr failed"))
	}
	add("journarr", "ok", fmt.Sprintf("%d request(s)", len(p.requestIDs)))
	if a.Publish != nil {
		for _, id := range p.requestIDs {
			a.Publish("request.removed", map[string]any{"id": id})
		}
	}
	return done(nil)
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
