package poll

import (
	"context"
	"testing"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/store"
)

// Sonarr's history carries the protocol as an enum string. Stored verbatim,
// 1 311 of 1 345 downloads ended up with source "1".
func TestHistoryNormalizeMapsProtocolEnum(t *testing.T) {
	p := &ArrHistoryPoller{Arr: &clients.Arr{Name: "sonarr"}}
	for enum, want := range map[string]string{"1": "usenet", "2": "torrent", "usenet": "usenet", "": ""} {
		kind, op := p.normalize(clients.HistoryRecord{
			ID: 1, EventType: "grabbed", DownloadID: "abc",
			Series: &clients.Series{ID: 5, TvdbID: 99, Title: "Show"},
			Data:   map[string]string{"protocol": enum},
		})
		if kind != "grab" {
			t.Fatalf("kind = %q, want grab", kind)
		}
		if got := op.(pipeline.GrabOp).Protocol; got != want {
			t.Errorf("protocol %q → %q, want %q", enum, got, want)
		}
	}
}

// The webhook writes the source first; the history replay of the same grab
// must not blank or downgrade it.
func TestUpsertDownloadKeepsKnownSource(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.UpsertDownload(ctx, store.Download{ClientDownloadID: "ABC", Arr: "sonarr", Source: "usenet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDownload(ctx, store.Download{ClientDownloadID: "abc", Arr: "sonarr", Source: ""}); err != nil {
		t.Fatal(err)
	}
	dl, err := st.FindDownloadByClientID(ctx, "abc")
	if err != nil || dl == nil || dl.ID != id {
		t.Fatalf("download lookup failed: %v %v", dl, err)
	}
	if dl.Source != "usenet" {
		t.Fatalf("source = %q after replay, want usenet", dl.Source)
	}
	// A stray enum on insert is normalized, never stored raw.
	if _, err := st.UpsertDownload(ctx, store.Download{ClientDownloadID: "def", Arr: "radarr", Source: "2"}); err != nil {
		t.Fatal(err)
	}
	if dl, _ := st.FindDownloadByClientID(ctx, "def"); dl == nil || dl.Source != "torrent" {
		t.Fatalf("source = %v, want torrent", dl)
	}
}
