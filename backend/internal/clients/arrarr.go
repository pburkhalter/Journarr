package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Arrarr talks to the TorBox shim. /status.json is unauthenticated and
// exercises the DB, so it doubles as the health probe; the API key is kept
// for the SAB history reconciler in later milestones.
type Arrarr struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewArrarr(baseURL, apiKey string, timeout time.Duration) *Arrarr {
	return &Arrarr{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

// StatusJSON mirrors arrarr's /status.json response.
type ArrarrStatus struct {
	GeneratedAt  string         `json:"generated_at"`
	Version      string         `json:"version"`
	States       map[string]int `json:"states"`
	TorboxCreate *struct {
		Available int `json:"available"`
		Capacity  int `json:"capacity"`
	} `json:"torbox_create,omitempty"`
}

func (c *Arrarr) Status(ctx context.Context) (*ArrarrStatus, time.Duration, error) {
	var body ArrarrStatus
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/status.json", nil, &body)
	if err != nil {
		return nil, lat, err
	}
	return &body, lat, nil
}

func (c *Arrarr) CheckHealth(ctx context.Context) HealthResult {
	body, lat, err := c.Status(ctx)
	if err != nil {
		return down(lat, err)
	}
	detail := map[string]any{"states": body.States}
	if body.TorboxCreate != nil {
		detail["torbox_create"] = map[string]any{
			"available": body.TorboxCreate.Available,
			"capacity":  body.TorboxCreate.Capacity,
		}
	}
	return HealthResult{Status: "up", Latency: lat, Version: body.Version, Detail: detail}
}
