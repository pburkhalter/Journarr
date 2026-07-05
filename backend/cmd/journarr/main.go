package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pburkhalter/journarr/internal/api"
	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/config"
	"github.com/pburkhalter/journarr/internal/logger"
	"github.com/pburkhalter/journarr/internal/poll"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/web"
)

var versionStr = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(versionStr)
			return
		case "healthcheck":
			os.Exit(healthcheck())
		case "help":
			fmt.Println("journarr [version|healthcheck|help]")
			return
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, cfg.LogFmt)
	log.Info("journarr starting", "version", versionStr, "listen", cfg.Listen)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	broker := api.NewBroker()

	checks := buildChecks(cfg)
	if len(checks) == 0 {
		log.Warn("no services configured — set SEERR_URL, SONARR_URL, … to monitor the stack")
	} else {
		hp := &poll.HealthPoller{
			Store:    st,
			Log:      log,
			Interval: cfg.HealthInterval,
			Checks:   checks,
			Publish:  broker.Publish,
		}
		go hp.Run(ctx)
		log.Info("health poller started", "services", len(checks), "interval", cfg.HealthInterval.String())
	}

	authn := auth.New(auth.Config{
		IssuerURL:     cfg.OIDCIssuerURL,
		ClientID:      cfg.OIDCClientID,
		ClientSecret:  cfg.OIDCClientSecret,
		PublicURL:     cfg.PublicURL,
		Scopes:        cfg.OIDCScopes,
		AllowedGroups: cfg.OIDCAllowedGroups,
		SessionSecret: cfg.SessionSecret,
		SessionMaxAge: cfg.SessionMaxAge,
	}, log)
	if authn.Enabled() {
		log.Info("sso enabled", "issuer", cfg.OIDCIssuerURL, "public_url", cfg.PublicURL)
	} else {
		log.Info("sso disabled — open access (set OIDC_ISSUER_URL to enable)")
	}

	router := api.NewRouter(api.Deps{
		Store:   st,
		Broker:  broker,
		Auth:    authn,
		Log:     log,
		Version: versionStr,
		Dist:    web.Dist(),
	})
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func buildChecks(cfg *config.Config) []poll.Check {
	t := cfg.UpstreamTimeout
	var checks []poll.Check
	if cfg.SeerrURL != "" {
		c := clients.NewSeerr(cfg.SeerrURL, cfg.SeerrAPIKey, t)
		checks = append(checks, poll.Check{Service: "seerr", Fn: c.CheckHealth})
	}
	if cfg.SonarrURL != "" {
		c := clients.NewArr("sonarr", cfg.SonarrURL, "/api/v3", cfg.SonarrAPIKey, t)
		checks = append(checks, poll.Check{Service: "sonarr", Fn: c.CheckHealth})
	}
	if cfg.RadarrURL != "" {
		c := clients.NewArr("radarr", cfg.RadarrURL, "/api/v3", cfg.RadarrAPIKey, t)
		checks = append(checks, poll.Check{Service: "radarr", Fn: c.CheckHealth})
	}
	if cfg.ProwlarrURL != "" {
		c := clients.NewArr("prowlarr", cfg.ProwlarrURL, "/api/v1", cfg.ProwlarrAPIKey, t)
		checks = append(checks, poll.Check{Service: "prowlarr", Fn: c.CheckHealth})
	}
	if cfg.ArrarrURL != "" {
		c := clients.NewArrarr(cfg.ArrarrURL, cfg.ArrarrAPIKey, t)
		checks = append(checks, poll.Check{Service: "arrarr", Fn: c.CheckHealth})
	}
	if cfg.JellyfinURL != "" {
		c := clients.NewJellyfin(cfg.JellyfinURL, cfg.JellyfinAPIKey, t)
		checks = append(checks, poll.Check{Service: "jellyfin", Fn: c.CheckHealth})
	}
	if cfg.WahaURL != "" {
		c := clients.NewWaha(cfg.WahaURL, cfg.WahaAPIKey, t)
		checks = append(checks, poll.Check{Service: "waha", Fn: c.CheckHealth})
	}
	return checks
}

// healthcheck probes the local /healthz — used as the Docker HEALTHCHECK
// command so the distroless image needs no curl/wget.
func healthcheck() int {
	listen := os.Getenv("JOURNARR_LISTEN")
	if listen == "" {
		listen = ":8484"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		fmt.Fprintln(os.Stderr, "healthcheck: cannot parse JOURNARR_LISTEN:", listen)
		return 1
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
