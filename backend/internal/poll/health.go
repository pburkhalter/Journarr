package poll

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/store"
)

// Check names one service probe.
type Check struct {
	Service string
	Fn      func(ctx context.Context) clients.HealthResult
}

// HealthPoller probes every configured service on a fixed interval, persists
// the result and publishes a service.health SSE event on each pass.
type HealthPoller struct {
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
	Checks   []Check
	Publish  func(event string, data any)
}

func (p *HealthPoller) Run(ctx context.Context) {
	p.pass(ctx)
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pass(ctx)
		}
	}
}

func (p *HealthPoller) pass(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range p.Checks {
		wg.Add(1)
		go func(c Check) {
			defer wg.Done()
			res := c.Fn(ctx)

			detail := ""
			if len(res.Detail) > 0 {
				if b, err := json.Marshal(res.Detail); err == nil {
					detail = string(b)
				}
			}
			h := store.ServiceHealth{
				Service:   c.Service,
				Status:    res.Status,
				LatencyMS: res.Latency.Milliseconds(),
				Version:   res.Version,
				Detail:    detail,
				CheckedAt: time.Now().UTC(),
			}
			if err := p.Store.UpsertServiceHealth(ctx, h); err != nil {
				p.Log.Error("health: persist failed", "service", c.Service, "err", err)
				return
			}
			if res.Status != "up" {
				p.Log.Warn("health: service not up", "service", c.Service, "status", res.Status, "detail", detail)
			}
			if p.Publish != nil {
				p.Publish("service.health", h)
			}
		}(c)
	}
	wg.Wait()
}
