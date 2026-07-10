package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Concierge probes waha-concierge's /streaming-status.json — a self-aggregated
// health view (its own issue list, grab-quota headroom, stuck-job count). With
// an API key it can also trigger the Journarr-owned notification send.
type Concierge struct {
	BaseURL string
	APIKey  string // token for POST /notify/send (Journarr-owned notifications)
	HTTP    *http.Client
}

func NewConcierge(baseURL, apiKey string, timeout time.Duration) *Concierge {
	return &Concierge{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

// Notification is a completion notice Journarr asks concierge to deliver.
type Notification struct {
	MediaType string          `json:"media_type"` // movie|tv
	TmdbID    int64           `json:"tmdb_id"`
	Title     string          `json:"title,omitempty"`
	Year      int64           `json:"year,omitempty"`
	Episodes  []NotifyEpisode `json:"episodes,omitempty"`
	PosterURL string          `json:"poster_url,omitempty"`
}

type NotifyEpisode struct {
	Season  int64  `json:"season"`
	Episode int64  `json:"episode"`
	Title   string `json:"title,omitempty"`
}

// SendNotification asks waha-concierge to deliver a WhatsApp completion notice
// (concierge does the formatting + requester @mention + Jellyfin deep link) and
// returns the message id.
func (c *Concierge) SendNotification(ctx context.Context, n Notification) (string, error) {
	body, err := json.Marshal(n)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/notify/send", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-Notify-Token", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("notify/send status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.MessageID, nil
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
	// WAHA is folded into the concierge tile (no standalone WAHA tile): surface
	// concierge's own view of the WhatsApp session here.
	if body.Metrics.Waha != "" {
		detail["waha"] = body.Metrics.Waha
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
