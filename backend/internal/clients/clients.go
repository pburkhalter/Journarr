// Package clients holds thin HTTP clients for every service Journarr
// observes. Each client exposes a CheckHealth used by the health poller;
// richer per-service methods (queues, history, items) arrive with the
// ingestion milestones.
package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HealthResult is the outcome of a single service probe.
type HealthResult struct {
	Status  string // up | degraded | down
	Latency time.Duration
	Version string
	Detail  map[string]any // serialized into service_health.detail
}

func newHTTP(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// getJSON fetches url, decodes the response into out and reports the request
// duration. Responses >= 400 are errors; bodies are capped at 4 MiB.
func getJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, out any) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := hc.Do(req)
	lat := time.Since(start)
	if err != nil {
		return lat, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return lat, err
	}
	if resp.StatusCode >= 400 {
		return lat, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if out == nil || len(raw) == 0 {
		return lat, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return lat, fmt.Errorf("decode: %w", err)
	}
	return lat, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func down(lat time.Duration, err error) HealthResult {
	return HealthResult{
		Status:  "down",
		Latency: lat,
		Detail:  map[string]any{"error": err.Error()},
	}
}
