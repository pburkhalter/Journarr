package clients

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Arrarr talks to the TorBox shim. /status.json is unauthenticated and
// exercises the DB, so it doubles as the health probe; the API key
// authenticates the SAB queue read that powers the live-downloads tile.
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

// arrarrQueue mirrors the SAB-shim /api?mode=queue response. arrarr only ever
// lists non-terminal jobs here, so every slot is an active download.
type arrarrQueue struct {
	Queue struct {
		SlotsTotal int `json:"noofslots_total"`
		Slots      []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"` // Queued | Downloading | Verifying
			Category string `json:"cat"`
		} `json:"slots"`
	} `json:"queue"`
}

// ArrarrDownload is one active job as surfaced on the service tile. arrarr has
// no byte-level progress (the TorBox webhook only fires on completion), so the
// honest signal is the coarse state, not a percentage.
type ArrarrDownload struct {
	Title string `json:"title"`
	State string `json:"state"` // downloading | importing | queued
}

// downloadsShown caps how many rows the tile carries; download_count keeps the
// true total so the UI can show "+N more".
const downloadsShown = 6

// statePriority orders the tile most-active first.
var statePriority = map[string]int{"downloading": 0, "importing": 1, "queued": 2}

// sabStatusToState maps the SAB slot status to our vocabulary. COMPLETED_TORBOX
// surfaces as SAB "Verifying" — TorBox is done and the puller is linking the
// file into the library, which reads more clearly as "importing".
func sabStatusToState(s string) string {
	switch s {
	case "Downloading":
		return "downloading"
	case "Verifying":
		return "importing"
	case "Queued":
		return "queued"
	default:
		return strings.ToLower(s)
	}
}

// Downloads reads the live SAB queue (active jobs only). Best-effort: it is
// never allowed to fail the health probe, so callers treat an error as "no
// activity to show" rather than "arrarr is down".
func (c *Arrarr) Downloads(ctx context.Context) (items []ArrarrDownload, total int, err error) {
	var q arrarrQueue
	if _, err = getJSON(ctx, c.HTTP, c.BaseURL+"/api?mode=queue&output=json",
		map[string]string{"X-Api-Key": c.APIKey}, &q); err != nil {
		return nil, 0, err
	}
	for _, s := range q.Queue.Slots {
		items = append(items, ArrarrDownload{Title: s.Filename, State: sabStatusToState(s.Status)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return statePriority[items[i].State] < statePriority[items[j].State]
	})
	total = q.Queue.SlotsTotal
	if total < len(items) {
		total = len(items)
	}
	if len(items) > downloadsShown {
		items = items[:downloadsShown]
	}
	return items, total, nil
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
	// Live per-item downloads (best-effort — a queue-read failure must not flip
	// the tile to "down"; arrarr itself answered /status.json fine).
	if items, total, derr := c.Downloads(ctx); derr == nil && len(items) > 0 {
		detail["downloads"] = items
		detail["download_count"] = total
	}
	return HealthResult{Status: "up", Latency: lat, Version: body.Version, Detail: detail}
}
