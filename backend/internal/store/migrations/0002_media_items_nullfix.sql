-- SQLite treats NULLs in UNIQUE constraints as distinct, so movie items
-- (season/episode NULL) were never deduplicated by the upsert. Rebuild with
-- NOT NULL sentinel -1. Pipeline tables are empty at this point (M1 ships
-- the first writers), so a plain drop+create is safe.
DROP TABLE media_items;
CREATE TABLE media_items (
  id                INTEGER PRIMARY KEY,
  request_id        INTEGER REFERENCES requests(id) ON DELETE CASCADE,
  media_type        TEXT NOT NULL CHECK(media_type IN ('movie','episode')),
  tmdb_id           INTEGER,
  tvdb_id           INTEGER,
  sonarr_series_id  INTEGER,
  sonarr_episode_id INTEGER,
  radarr_movie_id   INTEGER,
  season_number     INTEGER NOT NULL DEFAULT -1,
  episode_number    INTEGER NOT NULL DEFAULT -1,
  title             TEXT NOT NULL DEFAULT '',
  current_stage     TEXT NOT NULL DEFAULT 'requested' REFERENCES stages(key),
  current_cycle     INTEGER NOT NULL DEFAULT 1,
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
