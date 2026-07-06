package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
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

	ArrarrURL    string `env:"ARRARR_URL"`
	ArrarrAPIKey string `env:"ARRARR_API_KEY"`

	JellyfinURL    string `env:"JELLYFIN_URL"`
	JellyfinAPIKey string `env:"JELLYFIN_API_KEY"`
	JellyfinUserID string `env:"JELLYFIN_USER_ID"` // optional; unset uses the api-key-only /Items form

	WahaURL    string `env:"WAHA_URL"`
	WahaAPIKey string `env:"WAHA_API_KEY"`

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
