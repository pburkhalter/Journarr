// Package registry is the single source of truth for the services Journarr
// integrates with. Instances are declared via config (or synthesized from the
// legacy flat env vars) and each carries a Kind + a set of Capabilities. Every
// derived surface — the service tiles, the health checks, the available
// actions and which pipeline stages are active — reads from the registry
// instead of a hardcoded list.
package registry

// Capability is a declarative feature an instance provides. The UI and the
// control-plane switch on capabilities rather than on concrete types, so a new
// instance/type participates the moment it declares the right ones.
type Capability string

const (
	CapHealth         Capability = "health"          // exposes CheckHealth → a status tile
	CapIngestSource   Capability = "ingest-source"   // emits webhooks/poll data into the pipeline
	CapSearchMissing  Capability = "search-missing"  // arr: search for all monitored-missing
	CapSeasonSearch   Capability = "season-search"   // sonarr: SeasonSearch
	CapEpisodeSearch  Capability = "episode-search"  // sonarr: EpisodeSearch
	CapMovieSearch    Capability = "movie-search"    // radarr: MoviesSearch
	CapSeriesSearch   Capability = "series-search"   // sonarr: SeriesSearch
	CapRetry          Capability = "retry"           // re-grab a failed item
	CapCancel         Capability = "cancel"          // cancel a request end-to-end
	CapLibraryScan    Capability = "library-scan"    // jellyfin: RefreshLibrary
	CapNowPlaying     Capability = "now-playing"      // jellyfin: active playback sessions
	CapTranscodeStage Capability = "transcode-stage"  // tdarr: drives the transcode stage
	CapTranscodeScan  Capability = "transcode-scan"   // tdarr: rescan libraries
	CapTranscodePause Capability = "transcode-pause"  // tdarr: pause/resume transcode workers
	CapNotifySend     Capability = "notify-send"      // concierge: outbound notification send
)

// Kind is the concrete integration type an instance is built from.
type Kind string

const (
	KindSeerr     Kind = "seerr"
	KindSonarr    Kind = "sonarr"
	KindRadarr    Kind = "radarr"
	KindProwlarr  Kind = "prowlarr"
	KindArrarr    Kind = "arrarr"
	KindJellyfin  Kind = "jellyfin"
	KindWaha      Kind = "waha"
	KindConcierge Kind = "concierge"
	KindTdarr     Kind = "tdarr"
	KindJournarr  Kind = "journarr" // Journarr itself — a self-tile showing its own version
	KindGeneric   Kind = "generic"
)

// defaultCaps returns the capability set for a kind when a Spec doesn't
// declare its own.
func defaultCaps(k Kind) []Capability {
	switch k {
	case KindSonarr:
		return []Capability{CapHealth, CapIngestSource, CapSearchMissing, CapSeasonSearch, CapEpisodeSearch, CapSeriesSearch, CapRetry, CapCancel}
	case KindRadarr:
		return []Capability{CapHealth, CapIngestSource, CapSearchMissing, CapMovieSearch, CapRetry, CapCancel}
	case KindProwlarr:
		return []Capability{CapHealth}
	case KindArrarr:
		return []Capability{CapHealth, CapIngestSource}
	case KindJellyfin:
		return []Capability{CapHealth, CapLibraryScan, CapNowPlaying}
	case KindSeerr:
		return []Capability{CapHealth, CapIngestSource}
	case KindWaha:
		return []Capability{CapHealth}
	case KindConcierge:
		return []Capability{CapHealth, CapIngestSource, CapNotifySend}
	case KindTdarr:
		return []Capability{CapHealth, CapTranscodeStage, CapIngestSource, CapTranscodeScan, CapTranscodePause}
	case KindJournarr:
		return []Capability{CapHealth}
	default:
		return []Capability{CapHealth}
	}
}

// defaultStages returns the pipeline stage keys a kind can drive.
func defaultStages(k Kind) []string {
	if k == KindTdarr {
		return []string{"transcode"}
	}
	return nil
}

// defaultLabel is the human tile label fallback when a Spec omits one.
func defaultLabel(k Kind) string {
	switch k {
	case KindSeerr:
		return "Seerr"
	case KindSonarr:
		return "Sonarr"
	case KindRadarr:
		return "Radarr"
	case KindProwlarr:
		return "Prowlarr"
	case KindArrarr:
		return "Arrarr"
	case KindJellyfin:
		return "Jellyfin"
	case KindWaha:
		return "WAHA"
	case KindConcierge:
		return "Concierge"
	case KindTdarr:
		return "Tdarr"
	case KindJournarr:
		return "Journarr"
	default:
		return string(k)
	}
}

// kindOrder gives a stable default ordering that preserves the historical UI
// order (seerr, sonarr, radarr, prowlarr, arrarr, jellyfin, waha, concierge).
// Scaled by 10 so explicit per-instance Order values can interleave.
var kindOrder = map[Kind]int{
	KindSeerr:     10,
	KindSonarr:    20,
	KindRadarr:    30,
	KindProwlarr:  40,
	KindArrarr:    50,
	KindJellyfin:  60,
	KindTdarr:     70,
	KindWaha:      80,
	KindConcierge: 90,
	KindJournarr:  100,
}
