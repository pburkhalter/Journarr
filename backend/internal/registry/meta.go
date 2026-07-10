package registry

import "sort"

// InstanceMeta is the JSON-serializable view of an instance the frontend reads
// (via GET /api/instances) to drive tile ordering, labels and folding — so the
// UI no longer hardcodes the service list.
type InstanceMeta struct {
	ID       string       `json:"id"`
	Kind     Kind         `json:"kind"`
	Label    string       `json:"label"`
	Order    int          `json:"order"`
	ParentID string       `json:"parent_id,omitempty"`
	Caps     []Capability `json:"capabilities"`
	Stages   []string     `json:"stages,omitempty"`
}

// Meta returns instance metadata in display order.
func (r *Registry) Meta() []InstanceMeta {
	out := make([]InstanceMeta, 0, len(r.ordered))
	for _, i := range r.ordered {
		caps := make([]Capability, 0, len(i.Caps))
		for c := range i.Caps {
			caps = append(caps, c)
		}
		sort.Slice(caps, func(a, b int) bool { return caps[a] < caps[b] })
		out = append(out, InstanceMeta{
			ID:       i.ID,
			Kind:     i.Kind,
			Label:    i.Label,
			Order:    i.Order,
			ParentID: i.ParentID,
			Caps:     caps,
			Stages:   i.Stages,
		})
	}
	return out
}

// stageGates maps a stage key to the capability an instance must provide for
// that stage to be shown. Stages not listed are always active (when active=1
// in the catalog). This is how "which stages are active" derives from the
// configured instances — e.g. transcode only appears once a Tdarr instance
// exists (Phase 2), even after its catalog row is flipped active=1.
var stageGates = map[string]Capability{
	"transcode": CapTranscodeStage,
}

// StageActive reports whether a stage already marked active in the catalog
// should be exposed, given the configured instances.
func (r *Registry) StageActive(stageKey string) bool {
	cap, gated := stageGates[stageKey]
	if !gated {
		return true
	}
	return len(r.WithCapability(cap)) > 0
}
