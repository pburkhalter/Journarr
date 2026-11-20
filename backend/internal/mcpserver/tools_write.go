package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/clients"
)

func (s *Server) registerWriteTools() {
	addTool(s, &mcp.Tool{Name: "request_title", Annotations: mutating("Request a title", false, false),
		Description: "Request a movie or series through Seerr (approved right away, then Sonarr/Radarr " +
			"download it and Journarr tracks it). Series need seasons or all_seasons. quality_profile_id " +
			"and root_folder are optional (see list_options). Fails if Seerr already has the request."},
		true, s.requestTitle)

	addTool(s, &mcp.Tool{Name: "update_title", Annotations: mutating("Edit a library title", true, false),
		Description: "Change a movie or series in Sonarr/Radarr: monitored, quality_profile_id, " +
			"minimum_availability (movies: announced, inCinemas, released) or the monitored flag of " +
			"single seasons (series; also (un)monitors their episodes). Only the given fields change."},
		true, s.updateTitle)

	addTool(s, &mcp.Tool{Name: "search_title", Annotations: mutating("Search indexers now", false, false),
		Description: "Make Sonarr/Radarr search the indexers now for a movie, a whole series or one season."},
		true, s.searchTitle)

	addTool(s, &mcp.Tool{Name: "recheck_open_requests", Annotations: mutating("Re-check open requests", false, false),
		Description: "Square every open request with Sonarr/Radarr: items whose file is already on disk move " +
			"to imported, attempts whose every download failed start again, and every released or aired, " +
			"monitored, missing movie/episode gets one search (not again within 15 minutes). Returns per " +
			"request what it found: missing, waiting (not out yet), downloading, unmonitored, not_in_arr."},
		true, s.recheckOpen)

	addTool(s, &mcp.Tool{Name: "retry_item", Annotations: mutating("Retry a download", false, false),
		Description: "Retry a stuck or failed movie/episode (media_item_id from get_request): cancels and " +
			"blocklists its current download, then searches again."},
		true, s.retryItem)

	addTool(s, &mcp.Tool{Name: "cancel_request", Annotations: mutating("Cancel a request", true, false),
		Description: "Stop an active request: cancels its downloads, unmonitors it and removes the Seerr " +
			"request. Files already imported stay. Only for active, partial or failed requests."},
		true, s.cancelRequest)

	addTool(s, &mcp.Tool{Name: "resend_notification", Annotations: mutating("Resend the notice", true, false),
		Description: "Send the WhatsApp 'available' notice again for items that were never announced."},
		true, s.resendNotification)

	addTool(s, &mcp.Tool{Name: "scan_jellyfin", Annotations: mutating("Scan Jellyfin", true, false),
		Description: "Start a Jellyfin library scan (when a title is on disk but not shown in Jellyfin)."},
		true, s.scanJellyfin)
}

type requestTitleIn struct {
	MediaType        string  `json:"media_type" jsonschema:"movie or tv"`
	TmdbID           int64   `json:"tmdb_id" jsonschema:"TMDB id from search_titles"`
	Seasons          []int64 `json:"seasons,omitempty" jsonschema:"season numbers to request (series)"`
	AllSeasons       bool    `json:"all_seasons,omitempty" jsonschema:"request every season (series)"`
	QualityProfileID int64   `json:"quality_profile_id,omitempty"`
	RootFolder       string  `json:"root_folder,omitempty"`
}

func (s *Server) requestTitle(ctx context.Context, in requestTitleIn) (any, error) {
	created, err := s.d.Actions.RequestTitle(ctx, actions.TitleRequest(in))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"seerr_request_id": created.ID,
		"request_status":   seerrRequestStatus(created.Status),
		"media_status":     clients.SeerrMediaStatus(created.Media.Status),
		"note":             "Journarr picks the request up within a few minutes; follow it with list_requests.",
	}, nil
}

func seerrRequestStatus(code int) string {
	switch code {
	case 1:
		return "pending_approval"
	case 2:
		return "approved"
	case 3:
		return "declined"
	case 4:
		return "failed"
	case 5:
		return "completed"
	default:
		return "unknown"
	}
}

type updateTitleIn struct {
	TitleRef
	Monitored           *bool                   `json:"monitored,omitempty"`
	QualityProfileID    *int64                  `json:"quality_profile_id,omitempty"`
	MinimumAvailability *string                 `json:"minimum_availability,omitempty" jsonschema:"movies: announced, inCinemas or released"`
	Seasons             []actions.SeasonMonitor `json:"seasons,omitempty" jsonschema:"series: seasons to (un)monitor"`
}

func (s *Server) updateTitle(ctx context.Context, in updateTitleIn) (any, error) {
	e, err := s.d.Actions.UpdateTitle(ctx, in.title(), actions.TitleUpdate{
		Monitored: in.Monitored, QualityProfileID: in.QualityProfileID,
		MinimumAvailability: in.MinimumAvailability, Seasons: in.Seasons,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "library": s.libraryView(ctx, e)}, nil
}

type searchTitleIn struct {
	TitleRef
	Season *int64 `json:"season,omitempty" jsonschema:"series: search only this season"`
}

func (s *Server) searchTitle(ctx context.Context, in searchTitleIn) (any, error) {
	if err := s.d.Actions.SearchTitle(ctx, in.title(), in.Season); err != nil {
		return nil, err
	}
	return map[string]any{"searching": true}, nil
}

func (s *Server) recheckOpen(ctx context.Context, _ noArgs) (any, error) {
	return s.d.Actions.RecheckOpen(ctx)
}

type itemIn struct {
	MediaItemID int64 `json:"media_item_id" jsonschema:"from get_request"`
}

func (s *Server) retryItem(ctx context.Context, in itemIn) (any, error) {
	if in.MediaItemID == 0 {
		return nil, errors.New("media_item_id is required")
	}
	if err := s.d.Actions.Retry(ctx, in.MediaItemID); err != nil {
		return nil, err
	}
	return map[string]any{"retrying": true}, nil
}

type requestIn struct {
	RequestID int64 `json:"request_id" jsonschema:"from list_requests"`
}

func (s *Server) cancelRequest(ctx context.Context, in requestIn) (any, error) {
	if in.RequestID == 0 {
		return nil, errors.New("request_id is required")
	}
	if err := s.d.Actions.Cancel(ctx, in.RequestID); err != nil {
		return nil, err
	}
	return map[string]any{"canceled": true}, nil
}

func (s *Server) resendNotification(ctx context.Context, in requestIn) (any, error) {
	if in.RequestID == 0 {
		return nil, errors.New("request_id is required")
	}
	if err := s.d.Actions.ResendNotify(ctx, in.RequestID); err != nil {
		return nil, err
	}
	return map[string]any{"queued": true}, nil
}

func (s *Server) scanJellyfin(ctx context.Context, _ noArgs) (any, error) {
	if err := s.d.Actions.JellyfinScan(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"scanning": true}, nil
}
