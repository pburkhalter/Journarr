// Package flow is Journarr's control-plane. The download pipeline runs
// autonomously; this layer only performs the interventions the operator opts
// into via flow_settings (scan-on-import, auto-retry-stuck, notify-on-complete).
// Stage transitions enqueue durable flow_tasks, drained by one worker with
// retry/backoff, so a crash never drops or double-runs an intervention.
package flow

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/store"
)

const maxAttempts = 4

type Controller struct {
	Store *store.Store
	Log   *slog.Logger
	Acts  *actions.Actions
	Tick  time.Duration // worker/sweeper cadence

	mu       sync.RWMutex
	settings map[string]string
}

func New(st *store.Store, log *slog.Logger, acts *actions.Actions, tick time.Duration) *Controller {
	if tick <= 0 {
		tick = 20 * time.Second
	}
	return &Controller{Store: st, Log: log, Acts: acts, Tick: tick, settings: map[string]string{}}
}

// Reload refreshes the in-memory settings cache (called at startup and after a
// PUT /api/flow). OnStageApplied reads the cache, never the DB.
func (c *Controller) Reload(ctx context.Context) error {
	m, err := c.Store.GetFlowSettings(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.settings = m
	c.mu.Unlock()
	return nil
}

func (c *Controller) get(key, def string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if v, ok := c.settings[key]; ok {
		return v
	}
	return def
}

func (c *Controller) on(key string) bool {
	switch c.get(key, "") {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

func (c *Controller) intVal(key string, def int) int {
	if n, err := strconv.Atoi(c.get(key, "")); err == nil {
		return n
	}
	return def
}

// OnStageApplied runs on the projector goroutine after every applied transition.
// It must stay cheap: a cached-settings read plus at most one enqueue.
func (c *Controller) OnStageApplied(itemID, reqID int64, stage string, cycle int) {
	ctx := context.Background()
	if stage == "imported" && c.on("jellyfin_scan_on_import") {
		// Coalesce a burst of season imports into one delayed scan.
		_, _ = c.Store.EnqueueFlowTask(ctx, "jellyfin_scan", "", 0, "", "jellyfin_scan",
			time.Now().Add(45*time.Second))
	}
}

// Run drains due tasks and applies time-based rules on each tick.
func (c *Controller) Run(ctx context.Context) {
	t := time.NewTicker(c.Tick)
	defer t.Stop()
	for {
		c.drain(ctx)
		c.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Controller) drain(ctx context.Context) {
	tasks, err := c.Store.ClaimFlowTasks(ctx, 20)
	if err != nil {
		c.Log.Warn("flow: claim tasks", "err", err)
		return
	}
	for _, task := range tasks {
		err := c.exec(ctx, task)
		if err == nil {
			_ = c.Store.FinishFlowTask(ctx, task.ID, "done")
			continue
		}
		if task.Attempts+1 >= maxAttempts {
			c.Log.Warn("flow: task exhausted", "kind", task.Kind, "target", task.TargetID, "err", err)
			_ = c.Store.FinishFlowTask(ctx, task.ID, "failed")
			continue
		}
		backoff := time.Duration(task.Attempts+1) * 2 * time.Minute
		c.Log.Info("flow: task retry", "kind", task.Kind, "attempt", task.Attempts+1, "err", err)
		_ = c.Store.RescheduleFlowTask(ctx, task.ID, time.Now().Add(backoff))
	}
}

func (c *Controller) exec(ctx context.Context, task store.FlowTask) error {
	switch task.Kind {
	case "jellyfin_scan":
		return c.Acts.JellyfinScan(ctx)
	case "retry":
		return c.Acts.Retry(ctx, task.TargetID)
	default:
		return fmt.Errorf("unknown flow task kind %q", task.Kind)
	}
}

// sweep applies the auto-retry-stuck rule. Clearing the stuck flag on enqueue
// bounds the retry cadence to the stuck threshold (MarkStuck re-flags later)
// instead of retrying every tick.
func (c *Controller) sweep(ctx context.Context) {
	secs := c.intVal("auto_retry_stuck_after_secs", 0)
	if secs <= 0 {
		return
	}
	items, err := c.Store.StuckItemsForRetry(ctx, time.Duration(secs)*time.Second)
	if err != nil {
		c.Log.Warn("flow: sweep stuck", "err", err)
		return
	}
	for _, it := range items {
		key := fmt.Sprintf("retry:%d:%d", it.ID, it.CurrentCycle)
		inserted, _ := c.Store.EnqueueFlowTask(ctx, "retry", "media_item", it.ID, "", key, time.Now())
		if inserted {
			_ = c.Store.ClearStuckItem(ctx, it.ID)
			c.Log.Info("flow: auto-retry stuck item", "item", it.ID, "stage", it.CurrentStage)
		}
	}
}
