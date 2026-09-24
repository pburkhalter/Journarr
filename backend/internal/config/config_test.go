package config

import (
	"testing"
	"time"
)

// The flow controller's delays (60s notify grouping, 45s scan coalescing) only
// hold if it ticks well below them. It used to borrow the 5m stuck interval,
// which delayed every WhatsApp notice by exactly five minutes.
func TestFlowTickDefaultIsBelowNotifyDelay(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.FlowTickInterval > 30*time.Second {
		t.Fatalf("FlowTickInterval = %s, want <= 30s", c.FlowTickInterval)
	}
	if c.FlowTickInterval == c.StuckPollInterval {
		t.Fatal("flow tick must not share the stuck poll interval")
	}
}
