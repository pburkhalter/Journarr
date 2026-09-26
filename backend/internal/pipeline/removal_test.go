package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/store"
)

// "Remove everywhere" deletes a title's requests. A grab or import for it that
// arrives afterwards (a late webhook, the history poller catching up) used to
// recreate the title as an orphan request.
func TestPurgedTitleStaysGoneOnLateEvents(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)

	series := &SeriesRef{SonarrID: 9, TvdbID: 555, Title: "Show"}
	other := &SeriesRef{SonarrID: 10, TvdbID: 777, Title: "Other"}
	emit(t, s, "sonarr", "grab", GrabOp{Arr: "sonarr", DownloadID: "pack-a", Series: series,
		Episodes: []EpisodeRef{{SonarrID: 1, Season: 1, Episode: 1}}})
	emit(t, s, "sonarr", "grab", GrabOp{Arr: "sonarr", DownloadID: "pack-b", Series: other,
		Episodes: []EpisodeRef{{SonarrID: 5, Season: 1, Episode: 1}}})
	p.drain(ctx)

	tvdb := int64(555)
	reqs, err := s.RequestsForTitle(ctx, "tv", nil, &tvdb)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("RequestsForTitle = %d, %v; want 1", len(reqs), err)
	}
	series_, _, _ := s.ArrIDsForRequests(ctx, []int64{reqs[0].ID})
	if len(series_) != 1 || series_[0] != 9 {
		t.Fatalf("sonarr series ids = %v, want [9]", series_)
	}
	if _, err := s.EnqueueFlowTask(ctx, "notify", "request", reqs[0].ID, "", store.NotifyTaskKey(reqs[0].ID), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeRequests(ctx, []int64{reqs[0].ID}, store.Tombstone{Title: "Show", SonarrSeriesIDs: series_}); err != nil {
		t.Fatal(err)
	}

	emit(t, s, "sonarr", "import", ImportOp{Arr: "sonarr", DownloadID: "pack-a", Series: series,
		Episodes: []EpisodeRef{{SonarrID: 1, Season: 1, Episode: 1}}})
	p.drain(ctx)

	if again, _ := s.RequestsForTitle(ctx, "tv", nil, &tvdb); len(again) != 0 {
		t.Fatalf("removed title came back as %d request(s)", len(again))
	}
	if dl, _ := s.FindDownloadByClientID(ctx, "pack-a"); dl != nil {
		t.Fatalf("download of the removed title still there: %+v", dl)
	}
	if dl, _ := s.FindDownloadByClientID(ctx, "pack-b"); dl == nil {
		t.Fatal("download of another title was deleted")
	}
	tvdbOther := int64(777)
	if keep, _ := s.RequestsForTitle(ctx, "tv", nil, &tvdbOther); len(keep) != 1 {
		t.Fatalf("other title: %d requests, want 1", len(keep))
	}
	var tasks int
	_ = s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM flow_tasks`).Scan(&tasks)
	if tasks != 0 {
		t.Fatalf("%d flow task(s) left for the removed request", tasks)
	}
}

func TestRemovedSeerrRequestIsIgnored(t *testing.T) {
	ctx := context.Background()
	p, s := testProjector(t)
	if err := s.PurgeRequests(ctx, []int64{999}, store.Tombstone{SeerrRequestIDs: []int64{42}}); err != nil {
		t.Fatal(err)
	}
	emit(t, s, "seerr", "request", SeerrOp{Kind: "approved", SeerrRequestID: 42, MediaType: "movie", TmdbID: 1, Title: "Gone"})
	p.drain(ctx)
	tmdb := int64(1)
	if reqs, _ := s.RequestsForTitle(ctx, "movie", &tmdb, nil); len(reqs) != 0 {
		t.Fatalf("seerr event recreated a removed request")
	}
}
