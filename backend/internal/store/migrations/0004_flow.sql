-- Control-plane: operator-tunable flow settings + a durable outbound task queue.
-- Journarr stays an observer of the download pipeline but OWNS the interventions
-- toggled here (notify-on-complete, scan-on-import, auto-retry-stuck). Tasks are
-- drained by a single worker, retried on failure, and deduped while pending.

CREATE TABLE flow_settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE flow_tasks (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL,                       -- jellyfin_scan | notify | retry
  target_type TEXT,
  target_id   INTEGER,
  payload     TEXT,
  status      TEXT NOT NULL DEFAULT 'pending',     -- pending | done | failed
  attempts    INTEGER NOT NULL DEFAULT 0,
  run_after   DATETIME,
  -- Unique only while set; cleared on finish so a later trigger can re-enqueue
  -- the same logical task (e.g. a fresh jellyfin_scan after the last one ran).
  dedupe_key  TEXT UNIQUE,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME
);

CREATE INDEX idx_flow_tasks_pending ON flow_tasks(status, run_after);
