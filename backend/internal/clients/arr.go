package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Arr covers the Servarr family: Sonarr/Radarr (api/v3) and Prowlarr (api/v1)
// share the system/status + health surface, so one client serves all three.
type Arr struct {
	Name    string // sonarr|radarr|prowlarr — used in error context only
	BaseURL string
	APIBase string // "/api/v3" or "/api/v1"
	APIKey  string
	HTTP    *http.Client
}

func NewArr(name, baseURL, apiBase, apiKey string, timeout time.Duration) *Arr {
	return &Arr{
		Name:    name,
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIBase: apiBase,
		APIKey:  apiKey,
		HTTP:    newHTTP(timeout),
	}
}

func (c *Arr) headers() map[string]string {
	return map[string]string{"X-Api-Key": c.APIKey}
}

type arrHealthEntry struct {
	Source  string `json:"source"`
	Type    string `json:"type"` // ok|notice|warning|error
	Message string `json:"message"`
}

func (c *Arr) CheckHealth(ctx context.Context) HealthResult {
	var status struct {
		Version string `json:"version"`
	}
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+c.APIBase+"/system/status", c.headers(), &status)
	if err != nil {
		return down(lat, err)
	}

	res := HealthResult{Status: "up", Latency: lat, Version: status.Version, Detail: map[string]any{}}

	var entries []arrHealthEntry
	if _, err := getJSON(ctx, c.HTTP, c.BaseURL+c.APIBase+"/health", c.headers(), &entries); err != nil {
		// status endpoint answered, health endpoint didn't — still up, note it
		res.Detail["health_error"] = err.Error()
		return res
	}
	warnings := []string{}
	for _, e := range entries {
		if e.Type == "warning" || e.Type == "error" {
			warnings = append(warnings, e.Message)
			if e.Type == "error" {
				res.Status = "degraded"
			}
		}
	}
	if len(warnings) > 0 {
		if res.Status == "up" {
			res.Status = "degraded"
		}
		res.Detail["health_warnings"] = len(warnings)
		if len(warnings) > 3 {
			warnings = warnings[:3]
		}
		res.Detail["health_messages"] = warnings
	}
	return res
}
