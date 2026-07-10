package registry

import (
	"fmt"
	"sort"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
)

// Spec is the declarative description of one instance, parsed from
// JOURNARR_INSTANCES (JSON) or synthesized from the legacy flat env vars.
type Spec struct {
	ID       string            `json:"id"`
	Kind     Kind              `json:"kind"`
	Label    string            `json:"label,omitempty"`
	URL      string            `json:"url"`
	APIKey   string            `json:"api_key,omitempty"`
	Caps     []Capability      `json:"capabilities,omitempty"`
	ParentID string            `json:"parent_id,omitempty"`
	Order    int               `json:"order,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

// Build constructs the registry from specs. Instances with an empty URL are
// skipped (mirrors the legacy "empty URL ⇒ not monitored" rule). IDs must be
// unique.
func Build(specs []Spec, timeout time.Duration) (*Registry, error) {
	r := &Registry{byID: map[string]*Instance{}}
	for _, s := range specs {
		if s.URL == "" {
			continue
		}
		if s.ID == "" {
			return nil, fmt.Errorf("instance of kind %q has empty id", s.Kind)
		}
		if _, dup := r.byID[s.ID]; dup {
			return nil, fmt.Errorf("duplicate instance id %q", s.ID)
		}
		inst, err := buildInstance(s, timeout)
		if err != nil {
			return nil, fmt.Errorf("instance %q: %w", s.ID, err)
		}
		r.byID[inst.ID] = inst
		r.ordered = append(r.ordered, inst)
	}
	sort.SliceStable(r.ordered, func(i, j int) bool { return r.ordered[i].Order < r.ordered[j].Order })
	return r, nil
}

func buildInstance(s Spec, timeout time.Duration) (*Instance, error) {
	caps := s.Caps
	if len(caps) == 0 {
		caps = defaultCaps(s.Kind)
	}
	capset := make(map[Capability]bool, len(caps))
	for _, c := range caps {
		capset[c] = true
	}
	label := s.Label
	if label == "" {
		label = defaultLabel(s.Kind)
	}
	order := s.Order
	if order == 0 {
		order = kindOrder[s.Kind]
	}
	inst := &Instance{
		ID:       s.ID,
		Kind:     s.Kind,
		Label:    label,
		Order:    order,
		ParentID: s.ParentID,
		Caps:     capset,
		Stages:   defaultStages(s.Kind),
	}

	switch s.Kind {
	case KindSonarr:
		inst.Client = clients.NewArr(s.ID, s.URL, "/api/v3", s.APIKey, timeout)
	case KindRadarr:
		inst.Client = clients.NewArr(s.ID, s.URL, "/api/v3", s.APIKey, timeout)
	case KindProwlarr:
		inst.Client = clients.NewArr(s.ID, s.URL, "/api/v1", s.APIKey, timeout)
	case KindArrarr:
		inst.Client = clients.NewArrarr(s.URL, s.APIKey, timeout)
	case KindJellyfin:
		j := clients.NewJellyfin(s.URL, s.APIKey, timeout)
		j.UserID = s.Extra["user_id"]
		inst.Client = j
	case KindSeerr:
		inst.Client = clients.NewSeerr(s.URL, s.APIKey, timeout)
	case KindWaha:
		inst.Client = clients.NewWaha(s.URL, s.APIKey, timeout)
	case KindConcierge:
		inst.Client = clients.NewConcierge(s.URL, timeout)
	case KindTdarr:
		inst.Client = clients.NewTdarr(s.URL, s.APIKey, timeout)
	default:
		return nil, fmt.Errorf("unsupported kind %q", s.Kind)
	}
	return inst, nil
}
