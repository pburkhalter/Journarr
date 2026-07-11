package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serveQueue returns an httptest server mimicking arrarr's SAB /api?mode=queue.
// It records whether the API key was presented via the X-Api-Key header.
func serveQueue(t *testing.T, apiKey string, slots []map[string]string, total int, sawKey *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.URL.Query().Get("mode") != "queue" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Api-Key") == apiKey {
			*sawKey = true
		}
		s := make([]map[string]any, 0, len(slots))
		for i, sl := range slots {
			s = append(s, map[string]any{
				"index": i, "filename": sl["filename"], "cat": sl["cat"], "status": sl["status"],
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"queue": map[string]any{"noofslots_total": total, "slots": s},
		})
	}))
}

func TestArrarrDownloads_MappingAndSort(t *testing.T) {
	var sawKey bool
	// Deliberately out of priority order to prove the sort.
	srv := serveQueue(t, "secret", []map[string]string{
		{"filename": "Queued.Show.S01E01", "status": "Queued", "cat": "tv"},
		{"filename": "Importing.Movie.2024", "status": "Verifying", "cat": "movies"},
		{"filename": "Downloading.Show.S02E03", "status": "Downloading", "cat": "tv"},
	}, 3, &sawKey)
	defer srv.Close()

	c := NewArrarr(srv.URL, "secret", 5*time.Second)
	items, total, err := c.Downloads(context.Background())
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	if !sawKey {
		t.Error("X-Api-Key header was not sent")
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	// Sorted most-active first: downloading, importing, queued.
	want := []ArrarrDownload{
		{Title: "Downloading.Show.S02E03", State: "downloading"},
		{Title: "Importing.Movie.2024", State: "importing"},
		{Title: "Queued.Show.S01E01", State: "queued"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(items), len(want), items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestArrarrDownloads_CapKeepsTrueTotal(t *testing.T) {
	var sawKey bool
	slots := make([]map[string]string, 0, 10)
	for i := 0; i < 10; i++ {
		slots = append(slots, map[string]string{"filename": "job", "status": "Downloading", "cat": "tv"})
	}
	srv := serveQueue(t, "k", slots, 10, &sawKey)
	defer srv.Close()

	items, total, err := c(srv).Downloads(context.Background())
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	if len(items) != downloadsShown {
		t.Errorf("list len = %d, want cap %d", len(items), downloadsShown)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10 (true count survives the cap)", total)
	}
}

func TestArrarrDownloads_Empty(t *testing.T) {
	var sawKey bool
	srv := serveQueue(t, "k", nil, 0, &sawKey)
	defer srv.Close()
	items, total, err := c(srv).Downloads(context.Background())
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Errorf("empty queue: items=%v total=%d, want 0/0", items, total)
	}
}

func c(srv *httptest.Server) *Arrarr { return NewArrarr(srv.URL, "k", 5*time.Second) }
