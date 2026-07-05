package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Seerr is a thin Jellyseerr/Seerr v1 API client.
type Seerr struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewSeerr(baseURL, apiKey string, timeout time.Duration) *Seerr {
	return &Seerr{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

func (c *Seerr) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["X-Api-Key"] = c.APIKey
	}
	return h
}

func (c *Seerr) CheckHealth(ctx context.Context) HealthResult {
	var body struct {
		Version       string `json:"version"`
		UpdateAvail   bool   `json:"updateAvailable"`
		RestartNeeded bool   `json:"restartRequired"`
	}
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v1/status", c.headers(), &body)
	if err != nil {
		return down(lat, err)
	}
	detail := map[string]any{}
	status := "up"
	if body.RestartNeeded {
		status = "degraded"
		detail["restart_required"] = true
	}
	return HealthResult{Status: status, Latency: lat, Version: body.Version, Detail: detail}
}
