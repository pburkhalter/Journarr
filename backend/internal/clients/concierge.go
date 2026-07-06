package clients

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Concierge probes waha-concierge's /streaming-status.json — a self-aggregated
// health view (its own issue list, grab-quota headroom, stuck-job count). It's
// unauthenticated and LAN-only, like the rest of the concierge surfaces.
type Concierge struct {
	BaseURL string
	HTTP    *http.Client
}

func NewConcierge(baseURL string, timeout time.Duration) *Concierge {
	return &Concierge{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: newHTTP(timeout)}
}

type conciergeStatus struct {
	OK         bool     `json:"ok"`
	IssueCount int      `json:"issue_count"`
	Issues     []string `json:"issues"`
	Metrics    struct {
		StuckJobs int    `json:"stuck_jobs"`
		Unflushed int    `json:"unflushed"`
		Waha      string `json:"waha"`
	} `json:"metrics"`
	SceneNZB struct {
		Indexer string `json:"indexer"`
		Used    int    `json:"used"`
		Cap     int    `json:"cap"`
		Left    int    `json:"left"`
	} `json:"scenenzb"`
}

func (c *Concierge) CheckHealth(ctx context.Context) HealthResult {
	var body conciergeStatus
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/streaming-status.json", nil, &body)
	if err != nil {
		return down(lat, err)
	}
	status := "up"
	if !body.OK || body.IssueCount > 0 {
		status = "degraded"
	}
	detail := map[string]any{
		"stuck_jobs": body.Metrics.StuckJobs,
		"unflushed":  body.Metrics.Unflushed,
	}
	if len(body.Issues) > 0 {
		detail["issues"] = body.Issues
	}
	if body.SceneNZB.Cap > 0 {
		detail["grab_quota"] = map[string]any{
			"indexer": body.SceneNZB.Indexer,
			"used":    body.SceneNZB.Used,
			"cap":     body.SceneNZB.Cap,
			"left":    body.SceneNZB.Left,
		}
	}
	return HealthResult{Status: status, Latency: lat, Detail: detail}
}
