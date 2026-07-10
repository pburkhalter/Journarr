package clients

import "context"

// Self is Journarr's own tile: it reports the running build version without a
// network call (Journarr can't meaningfully HTTP-probe itself). It satisfies
// the HealthChecker contract so the registry treats it like any other service.
type Self struct {
	Version string
}

func NewSelf(version string) *Self { return &Self{Version: version} }

func (s *Self) CheckHealth(_ context.Context) HealthResult {
	return HealthResult{Status: "up", Latency: 0, Version: s.Version, Detail: map[string]any{}}
}
