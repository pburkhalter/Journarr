package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serveArrHealth(t *testing.T, entries []arrHealthEntry) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "5.0.0"})
		case "/api/v3/health":
			_ = json.NewEncoder(w).Encode(entries)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestArrHealth_IndexerChecksAdvisory(t *testing.T) {
	indexerDown := []arrHealthEntry{
		{Source: "IndexerStatusCheck", Type: "error", Message: "All indexers are unavailable due to failures"},
		{Source: "UpdateCheck", Type: "warning", Message: "New update is available"},
	}
	cases := []struct {
		name     string
		entries  []arrHealthEntry
		advisory bool
		want     string
	}{
		{"indexer failure, no Prowlarr", indexerDown, false, "degraded"},
		{"indexer failure, Prowlarr reports it", indexerDown, true, "up"},
		{"other problem, Prowlarr in stack", append(indexerDown,
			arrHealthEntry{Source: "DownloadClientCheck", Type: "error", Message: "Unable to communicate with arrarr"}), true, "degraded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveArrHealth(t, tc.entries)
			defer srv.Close()
			c := NewArr("sonarr", srv.URL, "/api/v3", "key", 5*time.Second)
			c.IndexerChecksAdvisory = tc.advisory
			res := c.CheckHealth(context.Background())
			if res.Status != tc.want {
				t.Fatalf("status %q, want %q", res.Status, tc.want)
			}
			// The indexer message stays visible on the tile either way.
			msgs, _ := res.Detail["health_messages"].([]string)
			if len(msgs) == 0 || msgs[0] != indexerDown[0].Message {
				t.Fatalf("health_messages %v", res.Detail["health_messages"])
			}
		})
	}
}
