// Package audit carries the actor of an intervention (who clicked, which
// script or MCP client, or the control-plane) through a context, so the
// actions audit can record it without every caller passing it along.
package audit

import "context"

type ctxKey struct{}

// System is the actor when nothing set one (pollers, startup).
const System = "system"

// WithActor returns ctx tagged with actor. An empty actor leaves ctx as is.
func WithActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, actor)
}

// Actor returns the actor stored in ctx, or System.
func Actor(ctx context.Context) string {
	if s, ok := ctx.Value(ctxKey{}).(string); ok && s != "" {
		return s
	}
	return System
}
