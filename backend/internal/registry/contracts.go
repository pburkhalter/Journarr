package registry

import (
	"context"

	"github.com/pburkhalter/journarr/internal/clients"
)

// HealthChecker is the de-facto contract every tile-bearing client already
// satisfies (Arr, Jellyfin, Seerr, Arrarr, Waha, Notifyarr). Made explicit here
// so the health-poll wiring can be capability-driven instead of hardcoded.
type HealthChecker interface {
	CheckHealth(context.Context) clients.HealthResult
}

// The following runtime contracts are consumed by later phases (actions +
// control-plane). They are declared here so capability checks and the concrete
// clients stay in sync; existing clients already satisfy them.

// MissingSearcher issues arr /command calls (EpisodeSearch/SeasonSearch/…).
type MissingSearcher interface {
	Command(ctx context.Context, name string, extra map[string]any) error
}

// LibraryScanner triggers a media-server library refresh (Jellyfin).
type LibraryScanner interface {
	RefreshLibrary(ctx context.Context) error
}
