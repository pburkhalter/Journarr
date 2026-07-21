package registry

import "github.com/pburkhalter/journarr/internal/clients"

// Instance is one configured integration. Client holds the concrete typed
// client (*clients.Arr, *clients.Jellyfin, …) built by the factory.
type Instance struct {
	ID       string
	Kind     Kind
	Label    string
	Order    int
	ParentID string // non-empty ⇒ folds into the parent's tile (e.g. waha → notifyarr)
	Caps     map[Capability]bool
	Stages   []string
	Client   any
}

// Has reports whether the instance declares capability c.
func (i *Instance) Has(c Capability) bool { return i.Caps[c] }

// Registry is the built, ordered set of instances.
type Registry struct {
	ordered []*Instance
	byID    map[string]*Instance
}

// All returns every instance in display order.
func (r *Registry) All() []*Instance { return r.ordered }

// ByID returns the instance with the given id, or nil.
func (r *Registry) ByID(id string) *Instance { return r.byID[id] }

// ByKind returns all instances of a kind, in order.
func (r *Registry) ByKind(k Kind) []*Instance {
	var out []*Instance
	for _, i := range r.ordered {
		if i.Kind == k {
			out = append(out, i)
		}
	}
	return out
}

// WithCapability returns all instances that declare capability c, in order.
func (r *Registry) WithCapability(c Capability) []*Instance {
	var out []*Instance
	for _, i := range r.ordered {
		if i.Caps[c] {
			out = append(out, i)
		}
	}
	return out
}

// --- typed accessors used by the current wiring -------------------------------
// These preserve the historical "one sonarr / one radarr" assumption. N-instance
// media routing (an instance_id on downloads/media_items) is a later additive
// migration; the IDs are already carried here so it won't be a redesign.

func (r *Registry) firstArr(k Kind) *clients.Arr {
	for _, i := range r.ByKind(k) {
		if a, ok := i.Client.(*clients.Arr); ok {
			return a
		}
	}
	return nil
}

// Sonarr returns the first Sonarr client, or nil.
func (r *Registry) Sonarr() *clients.Arr   { return r.firstArr(KindSonarr) }
func (r *Registry) Prowlarr() *clients.Arr { return r.firstArr(KindProwlarr) }

// Radarr returns the first Radarr client, or nil.
func (r *Registry) Radarr() *clients.Arr { return r.firstArr(KindRadarr) }

// MediaArrs returns the Sonarr+Radarr clients (the download-tracking arrs that
// the history/queue pollers reconcile), in order. Prowlarr is health-only.
func (r *Registry) MediaArrs() []*clients.Arr {
	var out []*clients.Arr
	for _, i := range r.ordered {
		if i.Kind != KindSonarr && i.Kind != KindRadarr {
			continue
		}
		if a, ok := i.Client.(*clients.Arr); ok {
			out = append(out, a)
		}
	}
	return out
}

// Seerr returns the first Seerr client, or nil.
func (r *Registry) Seerr() *clients.Seerr {
	for _, i := range r.ByKind(KindSeerr) {
		if c, ok := i.Client.(*clients.Seerr); ok {
			return c
		}
	}
	return nil
}

// Jellyfin returns the first Jellyfin client, or nil.
func (r *Registry) Jellyfin() *clients.Jellyfin {
	for _, i := range r.ByKind(KindJellyfin) {
		if c, ok := i.Client.(*clients.Jellyfin); ok {
			return c
		}
	}
	return nil
}

// Arrarr returns the first arrarr client, or nil.
func (r *Registry) Arrarr() *clients.Arrarr {
	for _, i := range r.ByKind(KindArrarr) {
		if c, ok := i.Client.(*clients.Arrarr); ok {
			return c
		}
	}
	return nil
}

// Notifyarr returns the first notifyarr client, or nil.
func (r *Registry) Notifyarr() *clients.Notifyarr {
	for _, i := range r.ByKind(KindNotifyarr) {
		if c, ok := i.Client.(*clients.Notifyarr); ok {
			return c
		}
	}
	return nil
}

// Tdarr returns the first tdarr client, or nil.
func (r *Registry) Tdarr() *clients.Tdarr {
	for _, i := range r.ByKind(KindTdarr) {
		if c, ok := i.Client.(*clients.Tdarr); ok {
			return c
		}
	}
	return nil
}
