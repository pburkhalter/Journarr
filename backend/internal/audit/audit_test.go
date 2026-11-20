package audit

import (
	"context"
	"testing"
)

func TestActor(t *testing.T) {
	ctx := context.Background()
	if got := Actor(ctx); got != System {
		t.Fatalf("empty ctx: got %q, want %q", got, System)
	}
	if got := Actor(WithActor(ctx, "")); got != System {
		t.Fatalf("empty actor: got %q, want %q", got, System)
	}
	if got := Actor(WithActor(ctx, "mcp:mac")); got != "mcp:mac" {
		t.Fatalf("got %q, want mcp:mac", got)
	}
	// survives detaching from the request's cancellation
	if got := Actor(context.WithoutCancel(WithActor(ctx, "flow"))); got != "flow" {
		t.Fatalf("WithoutCancel: got %q, want flow", got)
	}
}
