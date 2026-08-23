package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// media_items.radarr_movie_id wird erst beim ersten Grab gesetzt. Ein Film, der
// nie gegriffen wurde, hat deshalb keine — und genau dann drueckt man Retry.
// Vorher brach das mit "no arr id on item %d to search" ab; jetzt wird die Id
// aus der tmdb_id aufgeloest und festgeschrieben.
func TestRetryResolvesRadarrMovieIDFromTmdb(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	const tmdb, radarrID = int64(1083381), int64(4242)
	var lookups int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/movie" && r.URL.Query().Get("tmdbId") == fmt.Sprint(tmdb) {
			lookups++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]clients.Movie{{ID: radarrID, TmdbID: tmdb, Title: "Backrooms"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	radarr := &clients.Arr{BaseURL: srv.URL, APIBase: "/api/v3", APIKey: "k",
		HTTP: &http.Client{Timeout: 5 * time.Second}}

	reqID, err := s.InsertOrphanRequest(ctx, "movie", "Backrooms", &[]int64{tmdb}[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := s.EnsureMediaItem(ctx, store.MediaItem{
		RequestID: &reqID, MediaType: "movie", TmdbID: &[]int64{tmdb}[0], Title: "Backrooms",
	})
	if err != nil {
		t.Fatal(err)
	}

	before, _ := s.GetMediaItem(ctx, itemID)
	if before.RadarrMovieID != nil {
		t.Fatalf("Vorbedingung verletzt: radarr_movie_id sollte leer sein, ist %v", *before.RadarrMovieID)
	}

	mv, err := radarr.MovieByTmdbID(ctx, tmdb)
	if err != nil || mv == nil {
		t.Fatalf("MovieByTmdbID: %v / %v", mv, err)
	}
	if mv.ID != radarrID {
		t.Fatalf("Radarr-Id = %d, erwartet %d", mv.ID, radarrID)
	}
	if err := s.SetItemRadarrMovieID(ctx, itemID, mv.ID); err != nil {
		t.Fatal(err)
	}

	after, _ := s.GetMediaItem(ctx, itemID)
	if after.RadarrMovieID == nil {
		t.Fatal("radarr_movie_id wurde nicht festgeschrieben")
	}
	if *after.RadarrMovieID != radarrID {
		t.Errorf("radarr_movie_id = %d, erwartet %d", *after.RadarrMovieID, radarrID)
	}
	if lookups != 1 {
		t.Errorf("Lookups = %d, erwartet genau 1", lookups)
	}
}

// Ist der Film nicht in Radarr, darf nichts geschrieben werden — sonst stuende
// eine erfundene Id in der Datenbank.
func TestMovieByTmdbIDReturnsNilWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	radarr := &clients.Arr{BaseURL: srv.URL, APIBase: "/api/v3", APIKey: "k",
		HTTP: &http.Client{Timeout: 5 * time.Second}}
	mv, err := radarr.MovieByTmdbID(context.Background(), 999)
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if mv != nil {
		t.Fatalf("erwartet nil, bekam %+v", mv)
	}
}
