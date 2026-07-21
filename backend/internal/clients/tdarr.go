package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
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

type tdarrNode struct {
	NodeName     string `json:"nodeName"`
	WorkerLimits struct {
		TranscodeGPU int `json:"transcodegpu"`
		TranscodeCPU int `json:"transcodecpu"`
	} `json:"workerLimits"`
	Workers map[string]struct {
		File       string  `json:"file"`
		FPS        float64 `json:"fps"`
		Percentage float64 `json:"percentage"`
		ETA        string  `json:"ETA"`
		JobType    string  `json:"jobType"`
	} `json:"workers"`
}

func (c *Tdarr) CheckHealth(ctx context.Context) HealthResult {
	// Version + reachability from /api/v2/status.
	var st struct {
		Version string `json:"version"`
	}
	_, _ = getJSON(ctx, c.HTTP, c.BaseURL+"/api/v2/status", nil, &st)
	// get-nodes returns the worker-node map on modern Tdarr, including each
	// worker's current file + progress — that's the live "what is it doing".
	var nodes map[string]tdarrNode
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v2/get-nodes", nil, &nodes)
	if err == nil {
		transcodes := []map[string]any{}
		gpuWorkers := 0
		for _, n := range nodes {
			gpuWorkers += n.WorkerLimits.TranscodeGPU
			for _, w := range n.Workers {
				if w.File == "" {
					continue
				}
				transcodes = append(transcodes, map[string]any{
					"file":       path.Base(w.File),
					"percentage": w.Percentage,
					"fps":        w.FPS,
					"eta":        w.ETA,
					"type":       w.JobType,
				})
			}
		}
		detail := map[string]any{"nodes": len(nodes), "gpu_workers": gpuWorkers}
		if len(transcodes) > 0 {
			detail["transcodes"] = transcodes
		}
		return HealthResult{Status: "up", Latency: lat, Version: st.Version, Detail: detail}
	}
	// Tdarr versions differ — fall back to a plain reachability probe on the
	// web root, which answers whenever the server is up.
	lat2, err2 := getJSON(ctx, c.HTTP, c.BaseURL+"/", nil, nil)
	if err2 != nil {
		return down(lat2, err2)
	}
	return HealthResult{Status: "up", Latency: lat2}
}

// Rescan re-scans every configured Tdarr library (queues new/changed files).
func (c *Tdarr) Rescan(ctx context.Context) error {
	var libs []struct {
		ID     string `json:"_id"`
		Folder string `json:"folder"`
	}
	if err := c.cruddb(ctx, "LibrarySettingsJSONDB", "getAll", "", nil, &libs); err != nil {
		return err
	}
	for _, l := range libs {
		body := map[string]any{"data": map[string]any{"scanConfig": map[string]any{
			"dbID": l.ID, "arrayOrPath": l.Folder, "mode": "scanFindNew",
		}}}
		if err := c.post(ctx, "/api/v2/scan-files", body, nil); err != nil {
			return err
		}
	}
	return nil
}

// SetTranscodeWorkers sets the GPU + CPU transcode worker limits on every node
// (0 = pause transcoding, ≥1 = run) by nudging Tdarr's alter-worker-limit
// endpoint up/down to the target. Used by the pause/resume action.
func (c *Tdarr) SetTranscodeWorkers(ctx context.Context, gpu, cpu int) error {
	var nodes map[string]tdarrNode
	if _, err := getJSON(ctx, c.HTTP, c.BaseURL+"/api/v2/get-nodes", nil, &nodes); err != nil {
		return err
	}
	for id, n := range nodes {
		if err := c.adjustWorker(ctx, id, "transcodegpu", n.WorkerLimits.TranscodeGPU, gpu); err != nil {
			return err
		}
		if err := c.adjustWorker(ctx, id, "transcodecpu", n.WorkerLimits.TranscodeCPU, cpu); err != nil {
			return err
		}
	}
	return nil
}

// adjustWorker steps a node's worker limit toward target one at a time.
func (c *Tdarr) adjustWorker(ctx context.Context, nodeID, workerType string, current, target int) error {
	dir := "increase"
	steps := target - current
	if steps < 0 {
		dir, steps = "decrease", -steps
	}
	for i := 0; i < steps; i++ {
		body := map[string]any{"data": map[string]any{
			"nodeID": nodeID, "process": dir, "workerType": workerType,
		}}
		if err := c.post(ctx, "/api/v2/alter-worker-limit", body, nil); err != nil {
			return err
		}
	}
	return nil
}

// post issues a JSON POST to the Tdarr API (LAN, unauthenticated).
func (c *Tdarr) post(ctx context.Context, apiPath string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+apiPath, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("tdarr %s: status %d: %s", apiPath, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// cruddb wraps Tdarr's generic /api/v2/cruddb store operations. getAll returns
// the collection as a JSON array.
func (c *Tdarr) cruddb(ctx context.Context, collection, mode, docID string, obj, out any) error {
	// Tdarr 2.82+ validates `obj` as an object and rejects null (getAll passes no
	// obj), so default it to an empty object.
	if obj == nil {
		obj = map[string]any{}
	}
	body := map[string]any{"data": map[string]any{
		"collection": collection, "mode": mode, "docID": docID, "obj": obj,
	}}
	return c.post(ctx, "/api/v2/cruddb", body, out)
}
