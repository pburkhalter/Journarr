package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Tdarr probes a Tdarr server — the optional transcode stage. Tdarr's API is
// LAN/unauthenticated by default; get-nodes returns the worker map, and any
// reachable response counts as up.
type Tdarr struct {
	BaseURL string
	APIKey  string // optional; Tdarr is typically unauthenticated on the LAN
	HTTP    *http.Client
}

func NewTdarr(baseURL, apiKey string, timeout time.Duration) *Tdarr {
	return &Tdarr{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

func (c *Tdarr) CheckHealth(ctx context.Context) HealthResult {
	// get-nodes returns the worker-node map on modern Tdarr.
	var nodes map[string]json.RawMessage
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v2/get-nodes", nil, &nodes)
	if err == nil {
		return HealthResult{Status: "up", Latency: lat, Detail: map[string]any{"nodes": len(nodes)}}
	}
	// Tdarr versions differ — fall back to a plain reachability probe on the
	// web root, which answers whenever the server is up.
	lat2, err2 := getJSON(ctx, c.HTTP, c.BaseURL+"/", nil, nil)
	if err2 != nil {
		return down(lat2, err2)
	}
	return HealthResult{Status: "up", Latency: lat2}
}
