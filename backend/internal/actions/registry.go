package actions

import (
	"context"
	"fmt"
	"sort"

	"github.com/pburkhalter/journarr/internal/registry"
)

// Descriptor is a UI-renderable action. The frontend lists them from
// GET /api/actions (derived from the registry's capabilities) and invokes them
// via POST /api/actions/execute.
type Descriptor struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`  // recheck-open|library-scan|transcode-*|cancel|resend-notify|season-search
	Scope      string `json:"scope"` // global|request|season|item
	InstanceID string `json:"instance_id,omitempty"`
	RequestID  int64  `json:"request_id,omitempty"`
	Season     *int64 `json:"season,omitempty"`
	Danger     bool   `json:"danger,omitempty"` // destructive → confirm in UI
}

// Available returns the actions valid for a scope/target, derived from the
// configured instances and their capabilities.
func (a *Actions) Available(ctx context.Context, scope string, targetID int64) []Descriptor {
	var out []Descriptor
	if a.Reg == nil {
		return out
	}
	switch scope {
	case "global":
		// (whole-library "search missing" removed — fetcharr owns that now;
		// the re-check searches only what open requests still miss)
		if a.Sonarr != nil || a.Radarr != nil {
			out = append(out, Descriptor{
				ID: "recheck-open", Label: "Re-check open requests",
				Kind: "recheck-open", Scope: "global",
			})
		}
		for _, inst := range a.Reg.WithCapability(registry.CapLibraryScan) {
			out = append(out, Descriptor{
				ID: "library-scan:" + inst.ID, Label: inst.Label + " library scan",
				Kind: "library-scan", Scope: "global", InstanceID: inst.ID,
			})
		}
		for _, inst := range a.Reg.WithCapability(registry.CapTranscodeScan) {
			out = append(out, Descriptor{
				ID: "transcode-scan:" + inst.ID, Label: inst.Label + " rescan",
				Kind: "transcode-scan", Scope: "global", InstanceID: inst.ID,
			})
		}
		for _, inst := range a.Reg.WithCapability(registry.CapTranscodePause) {
			out = append(out, Descriptor{
				ID: "transcode-pause:" + inst.ID, Label: "Pause " + inst.Label,
				Kind: "transcode-pause", Scope: "global", InstanceID: inst.ID,
			})
			out = append(out, Descriptor{
				ID: "transcode-resume:" + inst.ID, Label: "Resume " + inst.Label,
				Kind: "transcode-resume", Scope: "global", InstanceID: inst.ID,
			})
		}
	case "request":
		req, _ := a.Store.GetRequest(ctx, targetID)
		if req == nil {
			return out
		}
		if req.Status == "active" || req.Status == "partial" || req.Status == "failed" {
			out = append(out, Descriptor{
				ID: fmt.Sprintf("cancel:%d", targetID), Label: "Cancel request",
				Kind: "cancel", Scope: "request", RequestID: targetID, Danger: true,
			})
		}
		// Items that completed but were never announced (notifier outage).
		if len(a.Reg.WithCapability(registry.CapNotifySend)) > 0 && a.hasUnannounced(ctx, targetID) {
			out = append(out, Descriptor{
				ID: fmt.Sprintf("resend-notify:%d", targetID), Label: "Resend notification",
				Kind: "resend-notify", Scope: "request", RequestID: targetID,
			})
		}
		// Per-season search for tv requests (only when Sonarr can search).
		if len(a.Reg.WithCapability(registry.CapSeasonSearch)) > 0 {
			items, _ := a.Store.ListItemsForRequest(ctx, targetID)
			seen := map[int64]bool{}
			var seasons []int64
			for _, it := range items {
				if it.MediaType == "episode" && it.SeasonNumber != nil && !seen[*it.SeasonNumber] {
					seen[*it.SeasonNumber] = true
					seasons = append(seasons, *it.SeasonNumber)
				}
			}
			sort.Slice(seasons, func(i, j int) bool { return seasons[i] < seasons[j] })
			for _, s := range seasons {
				sc := s
				out = append(out, Descriptor{
					ID: fmt.Sprintf("season-search:%d:%d", targetID, s), Label: fmt.Sprintf("Search Season %d", s),
					Kind: "season-search", Scope: "season", RequestID: targetID, Season: &sc,
				})
			}
		}
	}
	return out
}

// Execute dispatches an action by kind. Params carry the target ids. The
// result is nil except for actions that report something (recheck-open).
func (a *Actions) Execute(ctx context.Context, kind string, params map[string]any) (any, error) {
	switch kind {
	case "recheck-open":
		rep, err := a.RecheckOpen(ctx)
		if rep == nil {
			return nil, err
		}
		return rep, err
	case "library-scan":
		return nil, a.JellyfinScan(ctx)
	case "transcode-scan":
		return nil, a.TdarrRescan(ctx)
	case "transcode-pause":
		return nil, a.TdarrWorkers(ctx, 0)
	case "transcode-resume":
		return nil, a.TdarrWorkers(ctx, 1)
	case "cancel":
		return nil, a.Cancel(ctx, pInt(params, "request_id"))
	case "retry":
		return nil, a.Retry(ctx, pInt(params, "media_item_id"))
	case "season-search":
		return nil, a.SeasonSearch(ctx, pInt(params, "request_id"), pInt(params, "season"))
	case "resend-notify":
		return nil, a.ResendNotify(ctx, pInt(params, "request_id"))
	default:
		return nil, fmt.Errorf("unknown action %q", kind)
	}
}

// SeasonSearch triggers Sonarr's SeasonSearch for the request's series + season.
func (a *Actions) SeasonSearch(ctx context.Context, requestID, season int64) error {
	id := a.begin(ctx, "season_search", "request", requestID, map[string]any{"season": season})
	if a.Sonarr == nil {
		return a.finish(ctx, id, fmt.Errorf("sonarr not configured"))
	}
	items, _ := a.Store.ListItemsForRequest(ctx, requestID)
	var seriesID int64
	for _, it := range items {
		if it.SonarrSeriesID != nil && *it.SonarrSeriesID > 0 {
			seriesID = *it.SonarrSeriesID
			break
		}
	}
	if seriesID == 0 {
		return a.finish(ctx, id, fmt.Errorf("no sonarr series id for request %d", requestID))
	}
	err := a.Sonarr.Command(ctx, "SeasonSearch", map[string]any{"seriesId": seriesID, "seasonNumber": season})
	if err == nil && a.Wake != nil {
		a.Wake()
	}
	return a.finish(ctx, id, err)
}

// pInt coerces a JSON param (numbers arrive as float64).
func pInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
