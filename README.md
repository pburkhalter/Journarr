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
JOURNARR_API_TOKEN=$(openssl rand -hex 32)         # optional, for scripts
```

With `JOURNARR_API_TOKEN` set, scripts call the API with
`Authorization: Bearer <token>` instead of a login session, e.g.
`curl -H "Authorization: Bearer $TOKEN" $JOURNARR_PUBLIC_URL/api/requests`.

Register the client in your IdP with callback URL
`$JOURNARR_PUBLIC_URL/auth/callback`. `/healthz` and `/webhook/*` stay
outside the SSO gate (`/webhook/*` is guarded by `JOURNARR_WEBHOOK_TOKEN`).

State-changing API calls (`POST`/`PUT`) from a browser session must send
`Content-Type: application/json` and come from the same origin
(`Sec-Fetch-Site`/`Origin`, with `JOURNARR_PUBLIC_URL` trusted behind a
proxy); calls with a bearer token are exempt from the content-type rule.
Every intervention lands in an audit with its actor (the SSO user's email,
`api-token`, `flow` or `anonymous` in open mode): `GET /api/actions/log`.

"Remove everywhere" is two steps: `GET /api/requests/{id}/removal` returns
the plan with a `plan_hash`; `POST /api/actions/remove` with that hash
deletes only if the plan is still the same, else it answers 409 with
`"code": "plan_changed"` (409 `"watching"` while someone plays the title,
unless `force`). The hash covers what gets deleted (Sonarr/Radarr entry and
folder, Journarr requests, Seerr entries), not file counts or downloads.

## MCP server (LLM access)

Journarr serves the [Model Context Protocol](https://modelcontextprotocol.io)
at `/mcp` (Streamable HTTP, stateless). An LLM client sees the pipeline and
the library and can change it — through the same audited actions the UI uses.

```
JOURNARR_MCP_TOKEN_MAC=$(openssl rand -hex 32)       # one token per client,
JOURNARR_MCP_TOKEN_HA=$(openssl rand -hex 32)        # the suffix names it
JOURNARR_MCP_WRITE=mac                               # clients that may change things
JOURNARR_MCP_REMOVE=off                              # off | approval (see below)
JOURNARR_MCP_WRITE_LIMIT=30                          # write calls per client per hour
```

Without any `JOURNARR_MCP_TOKEN_*` the endpoint does not exist. Tokens are
separate from `JOURNARR_API_TOKEN` and only valid on `/mcp`. A client only
sees the tools of its scope:

| Scope | Tools |
|---|---|
| read | `get_overview` (health with reasons, counts, stuck, disks), `list_requests`, `get_request`, `search_titles` (TMDB via Seerr, with status), `get_title`, `list_library`, `get_queue`, `get_calendar`, `list_options` (quality profiles, root folders) |
| write | `request_title` (through Seerr, so Journarr tracks it), `update_title` (monitored, quality profile, minimum availability, season monitoring), `search_title`, `recheck_open_requests` (square open requests with Sonarr/Radarr, take back items shown as available without a file, search what is out and missing), `retry_item`, `cancel_request`, `resend_notification`, `scan_jellyfin` |

Every call is logged with client and tool; every write lands in the audit
(`GET /api/actions/log`) with actor `mcp:<client>`.

```
claude mcp add --transport http journarr https://journarr.example.com/mcp \
  --header "Authorization: Bearer $JOURNARR_MCP_TOKEN_MAC"
```

### Removals need your approval

With `JOURNARR_MCP_REMOVE=approval` (needs SSO), write clients also get
`plan_removal`, `request_removal` and `get_removal_status`. A request stores
the removal plan the client saw and its `plan_hash`; **nothing is deleted**
until you approve it on the **Approvals** page (nav badge, also on the
phone). Approving needs an SSO session — the API token and every MCP token
get 403, and in open LAN mode nobody can approve. Approving re-plans and
deletes only if the hash still matches (else the request fails with
`plan_changed`). Requests expire after 72 h; there is no push notification
on purpose. There is no MCP tool that approves.

## Alerts

With Pushover credentials set, Journarr reports a service that stays **down**
for `ALERT_DOWN_AFTER` (default 10m) or **degraded** for
`ALERT_DEGRADED_AFTER` (default 2h) — one message per incident, one more if it
gets worse, one when it recovers. Pushover rather than WhatsApp on purpose:
the alert has to arrive when notifyarr or WAHA is what broke.

Two rules keep one problem to one message:

- `ALERT_SKIP_DEGRADED` (comma-separated service ids, default empty) leaves a
  service's degraded state to another notifier; down is still alerted. Example:
  `ALERT_SKIP_DEGRADED=waha` when Home Assistant already pushes a lost
  WhatsApp session.
- With a Prowlarr instance configured, Sonarr's and Radarr's `Indexer*` health
  checks stay on their tiles but do not make them degraded: Prowlarr reports
  the same indexer failure, so it alerts once instead of three times.

TrueNAS relay: TrueNAS 25.10 has no Pushover alert service. Add a **Slack**
alert service with the URL
`http://<journarr>:8484/webhook/truenas?token=$JOURNARR_WEBHOOK_TOKEN`; Journarr
forwards each alert text as a Pushover message titled "TrueNAS" (nothing is
stored). The route exists only when the Pushover values and the webhook token
are set.

```
PUSHOVER_TOKEN=…   # application token
PUSHOVER_USER=…    # user or group key
```

arrarr counts as down when its TorBox poller has not completed a pass for
15 minutes, even though it still answers.

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
