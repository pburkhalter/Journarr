package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/pburkhalter/journarr/internal/registry"
)

type Config struct {
	Listen   string `env:"JOURNARR_LISTEN" envDefault:":8484"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	LogFmt   string `env:"LOG_FORMAT" envDefault:"json"`

	DBPath string `env:"DB_PATH" envDefault:"/data/journarr.db"`

	// WebhookToken guards the inbound /webhook/{source} endpoints. Empty
	// disables webhook ingestion entirely (pollers still reconcile).
	WebhookToken string `env:"JOURNARR_WEBHOOK_TOKEN"`

	HealthInterval  time.Duration `env:"HEALTH_POLL_INTERVAL" envDefault:"45s"`
	UpstreamTimeout time.Duration `env:"UPSTREAM_TIMEOUT" envDefault:"10s"`

	SeerrPollInterval    time.Duration `env:"SEERR_POLL_INTERVAL" envDefault:"5m"`
	HistoryPollInterval  time.Duration `env:"ARR_HISTORY_POLL_INTERVAL" envDefault:"2m"`
	QueuePollInterval    time.Duration `env:"ARR_QUEUE_POLL_INTERVAL" envDefault:"60s"`
	JellyfinPollInterval time.Duration `env:"JELLYFIN_POLL_INTERVAL" envDefault:"2m"`
	PresencePollInterval time.Duration `env:"PRESENCE_POLL_INTERVAL" envDefault:"10m"`
	StuckPollInterval    time.Duration `env:"STUCK_POLL_INTERVAL" envDefault:"5m"`
	UpdateCheckInterval  time.Duration `env:"UPDATE_CHECK_INTERVAL" envDefault:"6h"`
	EventsRetentionDays  int           `env:"EVENTS_RETENTION_DAYS" envDefault:"90"`

	// Services with an empty URL are simply not monitored/ingested.
	SeerrURL    string `env:"SEERR_URL"`
	SeerrAPIKey string `env:"SEERR_API_KEY"`

	SonarrURL    string `env:"SONARR_URL"`
	SonarrAPIKey string `env:"SONARR_API_KEY"`

	RadarrURL    string `env:"RADARR_URL"`
	RadarrAPIKey string `env:"RADARR_API_KEY"`

	ProwlarrURL    string `env:"PROWLARR_URL"`
	ProwlarrAPIKey string `env:"PROWLARR_API_KEY"`
	// SceneNZB grab-quota, shown on the Prowlarr tile (moved off notifyarr).
	ProwlarrQuotaIndexer string `env:"PROWLARR_QUOTA_INDEXER" envDefault:"SceneNZB"`
	ProwlarrDailyCap     int    `env:"PROWLARR_DAILY_CAP" envDefault:"400"`

	ArrarrURL    string `env:"ARRARR_URL"`
	ArrarrAPIKey string `env:"ARRARR_API_KEY"`

	JellyfinURL    string `env:"JELLYFIN_URL"`
	JellyfinAPIKey string `env:"JELLYFIN_API_KEY"`
	JellyfinUserID string `env:"JELLYFIN_USER_ID"` // optional; unset uses the api-key-only /Items form

	WahaURL    string `env:"WAHA_URL"`
	WahaAPIKey string `env:"WAHA_API_KEY"`

	NotifyarrURL    string `env:"NOTIFYARR_URL"`
	NotifyarrAPIKey string `env:"NOTIFYARR_API_KEY"` // token for the /notify/send endpoint

	// Modular instance config. When set, JOURNARR_INSTANCES (inline JSON array)
	// or JOURNARR_INSTANCES_FILE (path to the same) fully replaces the flat env
	// vars above. Empty ⇒ the flat vars are synthesized into instances, so
	// existing deployments keep working unchanged.
	InstancesJSON string `env:"JOURNARR_INSTANCES"`
	InstancesFile string `env:"JOURNARR_INSTANCES_FILE"`

	// SSO via OIDC (Pocket ID, Authentik, …). Setting OIDC_ISSUER_URL turns
	// it on; without it Journarr is open (LAN mode).
	PublicURL         string        `env:"JOURNARR_PUBLIC_URL"`
	OIDCIssuerURL     string        `env:"OIDC_ISSUER_URL"`
	OIDCClientID      string        `env:"OIDC_CLIENT_ID"`
	OIDCClientSecret  string        `env:"OIDC_CLIENT_SECRET"`
	OIDCScopes        []string      `env:"OIDC_SCOPES" envSeparator:"," envDefault:"openid,profile,email,groups"`
	OIDCAllowedGroups []string      `env:"OIDC_ALLOWED_GROUPS" envSeparator:","`
	SessionSecret     string        `env:"SESSION_SECRET"`
	SessionMaxAge     time.Duration `env:"SESSION_MAX_AGE" envDefault:"168h"`
}

func (c *Config) SSOEnabled() bool { return c.OIDCIssuerURL != "" }

// InstanceSpecs returns the instance specs to build the registry from. If
// JOURNARR_INSTANCES[_FILE] is set it is parsed and takes full precedence;
// otherwise the legacy flat env vars are synthesized into equivalent specs so
// nothing changes on upgrade.
func (c *Config) InstanceSpecs() ([]registry.Spec, error) {
	raw := strings.TrimSpace(c.InstancesJSON)
	if raw == "" && c.InstancesFile != "" {
		b, err := os.ReadFile(c.InstancesFile)
		if err != nil {
			return nil, fmt.Errorf("read JOURNARR_INSTANCES_FILE: %w", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw != "" {
		var specs []registry.Spec
		if err := json.Unmarshal([]byte(raw), &specs); err != nil {
			return nil, fmt.Errorf("parse JOURNARR_INSTANCES: %w", err)
		}
		return specs, nil
	}
	return c.legacySpecs(), nil
}

// legacySpecs maps the flat per-service env vars onto instance specs, keeping
// the historical instance IDs (== old service names) so service_health rows and
// SSE keys are unchanged.
func (c *Config) legacySpecs() []registry.Spec {
	var specs []registry.Spec
	add := func(spec registry.Spec) {
		if spec.URL == "" {
			return
		}
		specs = append(specs, spec)
	}
	add(registry.Spec{ID: "seerr", Kind: registry.KindSeerr, URL: c.SeerrURL, APIKey: c.SeerrAPIKey})
	add(registry.Spec{ID: "sonarr", Kind: registry.KindSonarr, URL: c.SonarrURL, APIKey: c.SonarrAPIKey})
	add(registry.Spec{ID: "radarr", Kind: registry.KindRadarr, URL: c.RadarrURL, APIKey: c.RadarrAPIKey})
	add(registry.Spec{ID: "prowlarr", Kind: registry.KindProwlarr, URL: c.ProwlarrURL, APIKey: c.ProwlarrAPIKey})
	add(registry.Spec{ID: "arrarr", Kind: registry.KindArrarr, URL: c.ArrarrURL, APIKey: c.ArrarrAPIKey})
	add(registry.Spec{ID: "jellyfin", Kind: registry.KindJellyfin, URL: c.JellyfinURL, APIKey: c.JellyfinAPIKey,
		Extra: map[string]string{"user_id": c.JellyfinUserID}})
	// WAHA folds into the notifyarr tile: still health-checked, but ParentID
	// hides its standalone tile and its status shows inside the notifyarr card.
	// If notifyarr isn't configured, WAHA stands alone so its status isn't lost.
	wahaParent := ""
	if c.NotifyarrURL != "" {
		wahaParent = "notifyarr"
	}
	add(registry.Spec{ID: "waha", Kind: registry.KindWaha, URL: c.WahaURL, APIKey: c.WahaAPIKey, ParentID: wahaParent})
	add(registry.Spec{ID: "notifyarr", Kind: registry.KindNotifyarr, URL: c.NotifyarrURL, APIKey: c.NotifyarrAPIKey})
	return specs
}

func Load() (*Config, error) {
	c := &Config{}
	if err := env.Parse(c); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	for _, u := range []*string{
		&c.SeerrURL, &c.SonarrURL, &c.RadarrURL, &c.ProwlarrURL,
		&c.ArrarrURL, &c.JellyfinURL, &c.WahaURL,
	} {
		*u = strings.TrimRight(*u, "/")
	}
	if c.HealthInterval < 5*time.Second {
		return nil, fmt.Errorf("HEALTH_POLL_INTERVAL must be >= 5s")
	}
	if c.SSOEnabled() {
		if c.OIDCClientID == "" || c.OIDCClientSecret == "" {
			return nil, fmt.Errorf("OIDC_ISSUER_URL set — OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required")
		}
		if c.PublicURL == "" {
			return nil, fmt.Errorf("OIDC_ISSUER_URL set — JOURNARR_PUBLIC_URL is required (redirect URL base)")
		}
		if len(c.SessionSecret) < 16 {
			return nil, fmt.Errorf("OIDC_ISSUER_URL set — SESSION_SECRET of >= 16 chars is required")
		}
		c.PublicURL = strings.TrimRight(c.PublicURL, "/")
		c.OIDCIssuerURL = strings.TrimRight(c.OIDCIssuerURL, "/")
	}
	return c, nil
}
