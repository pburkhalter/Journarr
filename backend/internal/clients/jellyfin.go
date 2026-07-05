package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type Jellyfin struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewJellyfin(baseURL, apiKey string, timeout time.Duration) *Jellyfin {
	return &Jellyfin{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

func (c *Jellyfin) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["X-Emby-Token"] = c.APIKey
	}
	return h
}

func (c *Jellyfin) CheckHealth(ctx context.Context) HealthResult {
	var body struct {
		Version    string `json:"Version"`
		ServerName string `json:"ServerName"`
	}
	// Public endpoint — works without a token, token sent anyway when set.
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/System/Info/Public", c.headers(), &body)
	if err != nil {
		return down(lat, err)
	}
	return HealthResult{
		Status:  "up",
		Latency: lat,
		Version: body.Version,
		Detail:  map[string]any{"server_name": body.ServerName},
	}
}
