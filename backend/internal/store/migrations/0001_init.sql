-- Journarr initial schema: append-only events + derived pipeline state.

-- Table-driven stage catalog. 'transcode' ships inactive (Tdarr later):
-- flipping active=1 is the whole activation, no migration needed.
CREATE TABLE stages (
  key     TEXT PRIMARY KEY,
  ordinal INTEGER NOT NULL,
  label   TEXT NOT NULL,
  active  INTEGER NOT NULL DEFAULT 1
);
INSERT INTO stages(key, ordinal, label, active) VALUES
  ('requested',         10, 'Requested',       1),
  ('approved',          20, 'Approved',        1),
  ('grabbed',           30, 'Grabbed',         1),
  ('submitted',         40, 'Sent to TorBox',  1),
  ('cloud_downloading', 50, 'Cloud download',  1),
  ('pulling',           60, 'Pulling to NAS',  1),
  ('downloaded',        65, 'Downloaded',      1),
  ('imported',          70, 'Imported',        1),
  ('transcode',         80, 'Transcode',       0),
  ('available',         90, 'In Jellyfin',     1),
  ('notified',         100, 'Notified',        1);

CREATE TABLE requests (
  id               INTEGER PRIMARY KEY,
  seerr_request_id INTEGER UNIQUE,              -- NULL for arr-only "orphan" activity
  media_type       TEXT NOT NULL CHECK(media_type IN ('movie','tv')),
  tmdb_id          INTEGER,
  tvdb_id          INTEGER,
  title            TEXT NOT NULL DEFAULT '',
  year             INTEGER,
  poster_url       TEXT,
  requested_by     TEXT,
  requested_at     DATETIME,
  seasons          TEXT,                        -- JSON array of season numbers (tv)
  status           TEXT NOT NULL DEFAULT 'active'
    CHECK(status IN ('active','completed','partial','failed','canceled')),
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_requests_status ON requests(status);
CREATE INDEX idx_requests_tmdb ON requests(tmdb_id);

-- One row per trackable medium: a movie, or a single episode of a tv request.
CREATE TABLE media_items (
  id                INTEGER PRIMARY KEY,
  request_id        INTEGER REFERENCES requests(id) ON DELETE CASCADE,
  media_type        TEXT NOT NULL CHECK(media_type IN ('movie','episode')),
  tmdb_id           INTEGER,
  tvdb_id           INTEGER,
  sonarr_series_id  INTEGER,
  sonarr_episode_id INTEGER,
  radarr_movie_id   INTEGER,
  season_number     INTEGER,
  episode_number    INTEGER,
  title             TEXT NOT NULL DEFAULT '',
  current_stage     TEXT NOT NULL DEFAULT 'requested' REFERENCES stages(key),
  current_cycle     INTEGER NOT NULL DEFAULT 1,   -- bumped on retry/upgrade re-grabs
  stuck_since       DATETIME,
  last_error        TEXT,
  imported_path     TEXT,
  jellyfin_item_id  TEXT,
  notified_at       DATETIME,
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(request_id, media_type, season_number, episode_number)
);
CREATE INDEX idx_media_items_request ON media_items(request_id);
CREATE INDEX idx_media_items_epid ON media_items(sonarr_episode_id);
CREATE INDEX idx_media_items_movieid ON media_items(radarr_movie_id);
CREATE INDEX idx_media_items_stage ON media_items(current_stage);

-- One row per download-client job (an arrarr job). A season pack is one
-- download linked to N media_items via download_items.
CREATE TABLE downloads (
  id                 INTEGER PRIMARY KEY,
  client_download_id TEXT NOT NULL,   -- arr downloadId = arrarr nzo_id OR infohash (store lowercase)
  arrarr_nzo_id      TEXT,
  arr                TEXT NOT NULL CHECK(arr IN ('sonarr','radarr')),
  source             TEXT,            -- usenet|torrent
  release_title      TEXT,
  indexer            TEXT,
  size_bytes         INTEGER,
  state              TEXT NOT NULL DEFAULT 'grabbed'
    CHECK(state IN ('grabbed','submitted','cloud_downloading','pulling','downloaded','imported','failed','canceled')),
  bytes_downloaded   INTEGER,
  bytes_total        INTEGER,
  local_path         TEXT,
  last_error         TEXT,
  grabbed_at         DATETIME,
  completed_at       DATETIME,
  created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_downloads_cdi ON downloads(client_download_id);
CREATE INDEX idx_downloads_nzo ON downloads(arrarr_nzo_id);
CREATE INDEX idx_downloads_state ON downloads(state);

CREATE TABLE download_items (
  download_id   INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
  media_item_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  cycle         INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(download_id, media_item_id)
);
CREATE INDEX idx_download_items_media ON download_items(media_item_id);

-- Forward-only per cycle; the UNIQUE constraint is the projector's
-- idempotency guarantee (webhook + poller converge to one transition).
CREATE TABLE stage_transitions (
  id              INTEGER PRIMARY KEY,
  media_item_id   INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  cycle           INTEGER NOT NULL DEFAULT 1,
  stage           TEXT NOT NULL REFERENCES stages(key),
  entered_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  source_event_id INTEGER,
  note            TEXT,
  UNIQUE(media_item_id, cycle, stage)
);
CREATE INDEX idx_transitions_media ON stage_transitions(media_item_id, cycle);

-- Raw ingest log: every webhook delivery and poller observation lands here
-- first; the single projector goroutine folds them into the tables above.
CREATE TABLE events (
  id            INTEGER PRIMARY KEY,
  source        TEXT NOT NULL,     -- seerr|sonarr|radarr|arrarr|jellyfin|concierge|poller|action
  kind          TEXT NOT NULL,
  dedupe_key    TEXT UNIQUE,       -- NULL allowed (webhooks without natural ids)
  payload       TEXT NOT NULL,
  received_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  processed_at  DATETIME,
  match_status  TEXT NOT NULL DEFAULT 'pending'
    CHECK(match_status IN ('pending','matched','orphan','ignored')),
  request_id    INTEGER,
  media_item_id INTEGER,
  download_id   INTEGER
);
CREATE INDEX idx_events_unprocessed ON events(processed_at) WHERE processed_at IS NULL;
CREATE INDEX idx_events_media ON events(media_item_id);

CREATE TABLE service_health (
  service    TEXT PRIMARY KEY,     -- seerr|sonarr|radarr|prowlarr|arrarr|jellyfin|waha
  status     TEXT NOT NULL,        -- up|degraded|down
  latency_ms INTEGER,
  version    TEXT,
  detail     TEXT,                 -- JSON: torbox headroom, queue depth, indexer health, waha session
  checked_at DATETIME NOT NULL
);

CREATE TABLE actions (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL,       -- retry|cancel|jellyfin_scan|resend_notification
  target_type TEXT,
  target_id   INTEGER,
  status      TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','ok','failed')),
  detail      TEXT,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME
);

CREATE TABLE poll_state (
  source   TEXT PRIMARY KEY,
  cursor   TEXT,
  last_run DATETIME
);
