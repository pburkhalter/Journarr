package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type Waha struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewWaha(baseURL, apiKey string, timeout time.Duration) *Waha {
	return &Waha{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

func (c *Waha) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["X-Api-Key"] = c.APIKey
	}
	return h
}

func (c *Waha) CheckHealth(ctx context.Context) HealthResult {
	var sessions []struct {
		Name   string `json:"name"`
		Status string `json:"status"` // WORKING | STARTING | SCAN_QR_CODE | FAILED | STOPPED
	}
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/sessions", c.headers(), &sessions)
	if err != nil {
		return down(lat, err)
	}
	working := 0
	list := make([]map[string]string, 0, len(sessions))
	for _, s := range sessions {
		if s.Status == "WORKING" {
			working++
		}
		list = append(list, map[string]string{"name": s.Name, "status": s.Status})
	}
	status := "up"
	if working == 0 {
		// API reachable but no usable WhatsApp session (e.g. QR pairing pending)
		status = "degraded"
	}
	return HealthResult{Status: status, Latency: lat, Detail: map[string]any{"sessions": list}}
}
