// Package storage aggregates disk usage across every registered instance that
// declares CapDiskSpace.
//
// The numbers come from the arr APIs, not from the host Journarr runs on. That
// is deliberate: it needs no privileged access and no knowledge of the
// underlying platform, so it behaves identically on TrueNAS, Unraid, Synology
// or bare metal. Adding a new source is a matter of declaring the capability —
// this file does not know which kinds exist.
package storage

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pburkhalter/journarr/internal/registry"
)

// Mount is one deduplicated mount with the instances that reported it.
type Mount struct {
	Path        string   `json:"path"`
	Label       string   `json:"label,omitempty"`
	FreeSpace   int64    `json:"free_space"`
	TotalSpace  int64    `json:"total_space"`
	UsedSpace   int64    `json:"used_space"`
	UsedPercent float64  `json:"used_percent"`
	IsLibrary   bool     `json:"is_library"` // a configured root folder of some arr
	ReportedBy  []string `json:"reported_by"`
}

// instanceSource is the slice of the registry this package needs. Depending on
// the interface instead of *registry.Registry keeps the aggregator testable
// without exporting registry internals.
type instanceSource interface {
	WithCapability(registry.Capability) []*registry.Instance
}

// DefaultTTL is how long a reading stays fresh. The services page polls every
// 8s; without a cache every open tab would hammer each arr with two requests at
// that rate. Free space does not move fast enough to justify it.
const DefaultTTL = 60 * time.Second

type Service struct {
	Reg instanceSource
	Log *slog.Logger
	TTL time.Duration    // 0 = DefaultTTL
	Now func() time.Time // injectable for tests; nil = time.Now

	mu       sync.Mutex
	cached   []Mount
	cachedAt time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return DefaultTTL
}

// Mounts queries every capable instance and merges the results.
//
// Two instances on the same host report the same mounts (Sonarr and Radarr both
// see /media), so entries are keyed by path plus size — identical rows collapse
// into one and remember who reported them. Sizes are part of the key on purpose:
// same path with different totals is a real case (ZFS datasets with separate
// quotas share free space but not the total) and must not be silently merged.
// Results are cached for TTL. The lock is held across the HTTP calls on purpose:
// concurrent callers should wait for the one in-flight refresh rather than each
// start their own fan-out against the same arrs.
func (s *Service) Mounts(ctx context.Context) []Mount {
	if s.Reg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.now().Sub(s.cachedAt) < s.ttl() {
		return s.cached
	}

	type key struct {
		path        string
		free, total int64
	}
	merged := map[key]*Mount{}
	libs := map[string]bool{}

	for _, inst := range s.Reg.WithCapability(registry.CapDiskSpace) {
		rep, ok := inst.Client.(registry.DiskSpaceReporter)
		if !ok {
			continue
		}
		entries, err := rep.DiskSpace(ctx)
		if err != nil {
			// One unreachable instance must not blank the whole card.
			if s.Log != nil {
				s.Log.Debug("storage: diskspace", "instance", inst.ID, "err", err)
			}
			continue
		}
		if roots, err := rep.RootFolders(ctx); err == nil {
			for _, r := range roots {
				libs[normalize(r.Path)] = true
			}
		} else if s.Log != nil {
			s.Log.Debug("storage: rootfolders", "instance", inst.ID, "err", err)
		}

		for _, e := range entries {
			k := key{normalize(e.Path), e.FreeSpace, e.TotalSpace}
			m, seen := merged[k]
			if !seen {
				m = &Mount{Path: e.Path, Label: e.Label, FreeSpace: e.FreeSpace, TotalSpace: e.TotalSpace}
				merged[k] = m
			}
			if !contains(m.ReportedBy, inst.ID) {
				m.ReportedBy = append(m.ReportedBy, inst.ID)
			}
		}
	}

	out := make([]Mount, 0, len(merged))
	for _, m := range merged {
		m.UsedSpace = m.TotalSpace - m.FreeSpace
		if m.TotalSpace > 0 {
			m.UsedPercent = float64(m.UsedSpace) / float64(m.TotalSpace) * 100
		}
		m.IsLibrary = libs[normalize(m.Path)]
		out = append(out, *m)
	}
	// Library paths first (that is what people came to look at), then the
	// largest volumes; path as the tiebreak so the order never jitters.
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsLibrary != out[j].IsLibrary {
			return out[i].IsLibrary
		}
		if out[i].TotalSpace != out[j].TotalSpace {
			return out[i].TotalSpace > out[j].TotalSpace
		}
		return out[i].Path < out[j].Path
	})

	// An empty result is not cached: it means every instance was unreachable (or
	// none is capable, which costs nothing to recompute). Caching it would keep
	// the card blank for a full TTL after the arrs come back.
	if len(out) > 0 {
		s.cached, s.cachedAt = out, s.now()
	}
	return out
}

// normalize makes paths comparable across instances: a trailing slash is not a
// different mount.
func normalize(p string) string {
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
