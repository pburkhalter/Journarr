# Journarr

Every media request, end to end. Journarr tracks each request through the
whole pipeline — Seerr → Sonarr/Radarr → [Arrarr](https://github.com/pburkhalter/Arrarr)/TorBox
→ import → Jellyfin → WhatsApp notification — and shows the live health of
every tool along the way.

> _Journey + journal. A request travels through the stack; Journarr keeps the
> log of the trip._

## Pipeline stages

```
requested → approved → grabbed → submitted → cloud_downloading → pulling
         → downloaded → imported → [transcode*] → available → notified
```

`*` transcode (Tdarr) ships as an inactive placeholder stage — flipping it on
is a data change, not a schema migration.

## Status

Early days — milestone 0 of 4:

- [x] **M0** — service health grid (Seerr, Sonarr, Radarr, Prowlarr, Arrarr
      incl. TorBox headroom, Jellyfin, WAHA), SSE live updates, optional OIDC
      SSO (Pocket ID & friends), single-binary deploy with embedded frontend
- [ ] **M1** — request pipeline through import (Seerr/Sonarr/Radarr webhooks +
      reconciling pollers, flow board, request detail)
- [ ] **M2** — Arrarr transitions webhook, Jellyfin availability matching,
      WAHA notified stage: the full 10-stage timeline
- [ ] **M3** — actions (retry, cancel, Jellyfin scan, re-notify), stuck
      detection, history/search
- [ ] **M4** — hardening: basic auth, backfill, docs

## Architecture

Single Go binary (chi + SQLite + slog), Svelte 5 SPA embedded via `go:embed`.
Webhooks land in an append-only `events` table; pollers reconcile what
webhooks miss; a single projector folds both into per-item stage timelines
and fans out patches over SSE.

```
webhooks ─┐
          ├─> events (append-only) ─> projector ─> derived state ─> SSE ─> UI
pollers ──┘
```

## Run

```yaml
# see deploy/docker-compose.example.yaml for the full list
services:
  journarr:
    image: ghcr.io/pburkhalter/journarr:latest
    ports: ["8484:8484"]
    environment:
      SEERR_URL: http://192.168.0.203:5055
      SONARR_URL: http://192.168.0.203:8989
      # … any service with an empty URL is simply not monitored
    volumes:
      - /opt/docker-data/journarr:/data
```

## SSO

Optional OIDC login, built for [Pocket ID](https://pocket-id.org) but
provider-agnostic (auth code flow + PKCE via `go-oidc`/`x/oauth2`, encrypted
cookie sessions via `gorilla/sessions`). Without `OIDC_ISSUER_URL` Journarr
runs open — fine behind a LAN.

```
JOURNARR_PUBLIC_URL=https://journarr.example.com   # redirect base
OIDC_ISSUER_URL=https://id.example.com
OIDC_CLIENT_ID=…
OIDC_CLIENT_SECRET=…
SESSION_SECRET=$(openssl rand -hex 32)
OIDC_ALLOWED_GROUPS=journarr-users                 # optional
```

Register the client in your IdP with callback URL
`$JOURNARR_PUBLIC_URL/auth/callback`. `/healthz` and (later) `/webhook/*`
stay token-guarded outside the SSO gate.

## Develop

```
make backend-dev    # Go API on :8484
make frontend-dev   # Vite dev server on :5173, proxies /api to :8484
make build          # production binary with embedded frontend
make check          # go vet + go test + svelte-check
```

Layout: `backend/` (Go, module github.com/pburkhalter/journarr) and
`frontend/` (SvelteKit static SPA) — the frontend builds straight into
`backend/internal/web/dist` for embedding.

## License

MIT
