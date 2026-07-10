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

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/api"
	"github.com/pburkhalter/journarr/internal/auth"
	"github.com/pburkhalter/journarr/internal/config"
	"github.com/pburkhalter/journarr/internal/flow"
	"github.com/pburkhalter/journarr/internal/ingest"
	"github.com/pburkhalter/journarr/internal/logger"
	"github.com/pburkhalter/journarr/internal/pipeline"
	"github.com/pburkhalter/journarr/internal/poll"
	"github.com/pburkhalter/journarr/internal/registry"
	"github.com/pburkhalter/journarr/internal/store"
	"github.com/pburkhalter/journarr/internal/updates"
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

	specs, err := cfg.InstanceSpecs()
	if err != nil {
		return err
	}
	reg, err := registry.Build(specs, cfg.UpstreamTimeout)
	if err != nil {
		return err
	}
	sonarr, radarr, seerr, jelly := reg.Sonarr(), reg.Radarr(), reg.Seerr(), reg.Jellyfin()

	// Health checks derive from every instance that declares CapHealth and
	// whose client implements the HealthChecker contract.
	var checks []poll.Check
	for _, inst := range reg.WithCapability(registry.CapHealth) {
		if hc, ok := inst.Client.(registry.HealthChecker); ok {
			checks = append(checks, poll.Check{Service: inst.ID, Fn: hc.CheckHealth})
		}
	}
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

	// Pipeline: projector + ingestion + reconciling pollers. projector.Run is
	// started later, once the control-plane OnStage hook is wired (avoids a
	// data race on the field). Pollers/ingest may Wake it before then — the
	// buffered wake channel holds the signal until Run starts.
	projector := pipeline.New(st, log, broker.Publish, sonarr, radarr)

	var ing *ingest.Handler
	if cfg.WebhookToken != "" {
		ing = &ingest.Handler{Store: st, Log: log, Token: cfg.WebhookToken, Wake: projector.Wake}
		log.Info("webhook ingestion enabled", "sources", "seerr, sonarr, radarr")
	} else {
		log.Warn("JOURNARR_WEBHOOK_TOKEN not set — webhook ingestion disabled, pollers only")
	}

	if seerr != nil {
		go (&poll.SeerrRequestPoller{
			Store: st, Log: log, Seerr: seerr,
			Interval: cfg.SeerrPollInterval, Wake: projector.Wake,
		}).Run(ctx)
	}
	mediaArrs := reg.MediaArrs()
	for _, arr := range mediaArrs {
		go (&poll.ArrHistoryPoller{
			Store: st, Log: log, Arr: arr,
			Interval: cfg.HistoryPollInterval, Wake: projector.Wake,
		}).Run(ctx)
	}
	if len(mediaArrs) > 0 {
		go (&poll.ArrQueuePoller{
			Store: st, Log: log, Arrs: mediaArrs,
			Interval: cfg.QueuePollInterval, Publish: broker.Publish,
		}).Run(ctx)
	}
	if jelly != nil {
		go (&poll.JellyfinPoller{
			Store: st, Log: log, Jelly: jelly,
			Interval: cfg.JellyfinPollInterval, Wake: projector.Wake,
		}).Run(ctx)
	}
	// Presence reconciler: advance already-on-disk (arr hasFile) items that
	// pre-date Journarr or aged out of the Jellyfin recent window.
	if sonarr != nil || radarr != nil {
		go (&poll.PresencePoller{
			Store: st, Log: log, Sonarr: sonarr, Radarr: radarr,
			Interval: cfg.PresencePollInterval, Wake: projector.Wake,
		}).Run(ctx)
	}

	// Daily events reaper.
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := st.ReapEvents(ctx, cfg.EventsRetentionDays); err == nil && n > 0 {
					log.Info("reaper: pruned events", "count", n)
				}
			}
		}
	}()

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

	acts := &actions.Actions{
		Store: st, Log: log,
		Sonarr: sonarr, Radarr: radarr, Seerr: seerr, Jelly: jelly,
		Reg:  reg,
		Wake: projector.Wake, Publish: broker.Publish,
	}

	// Control-plane: owns the opt-in interventions (flow_settings). It observes
	// stage transitions via projector.OnStage and drains durable flow_tasks with
	// retry/backoff. All settings default off, so behavior is unchanged until a
	// user enables one in the Flow menu.
	flowCtrl := flow.New(st, log, acts, cfg.StuckPollInterval)
	if err := flowCtrl.Reload(ctx); err != nil {
		log.Warn("flow: initial settings load failed", "err", err)
	}
	projector.OnStage = flowCtrl.OnStageApplied
	go projector.Run(ctx)
	go flowCtrl.Run(ctx)

	// GitHub update checker for the self-hosted custom stack. Only services
	// that expose a semver build on their health surface can be compared;
	// arrarr does (via /status.json version). concierge/journarr can be added
	// here once they surface their version too.
	updateRepos := map[string]string{}
	if reg.Arrarr() != nil {
		updateRepos["arrarr"] = "pburkhalter/arrarr"
	}
	var updateChecker *updates.Checker
	if len(updateRepos) > 0 {
		updateChecker = updates.NewChecker(st, log, cfg.UpdateCheckInterval, updateRepos)
		go updateChecker.Run(ctx)
	}

	// Stuck sweeper: flag active items that stopped progressing. First run
	// delayed so the presence reconciler clears already-present items first.
	go func() {
		thresholds := map[string]int{
			"approved": 14400, "grabbed": 7200, "submitted": 3600,
			"cloud_downloading": 21600, "pulling": 3600, "downloaded": 1800, "imported": 1800,
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Minute):
		}
		t := time.NewTicker(cfg.StuckPollInterval)
		defer t.Stop()
		for {
			if n, err := st.MarkStuck(ctx, thresholds); err != nil {
				log.Warn("stuck sweep", "err", err)
			} else if n > 0 {
				log.Info("stuck sweep: newly flagged", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	router := api.NewRouter(api.Deps{
		Store:    st,
		Broker:   broker,
		Auth:     authn,
		Ingest:   ing,
		Actions:  acts,
		Registry: reg,
		Flow:     flowCtrl,
		Updates:  updateChecker,
		Log:      log,
		Version:  versionStr,
		Dist:     web.Dist(),
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
	broker.Shutdown() // release SSE handlers so Shutdown() can drain
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
