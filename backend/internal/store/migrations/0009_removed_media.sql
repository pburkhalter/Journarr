-- Tombstones for titles removed everywhere ("Remove everywhere" action). A
-- late webhook or history record for a removed series/movie would otherwise
-- recreate it as an orphan request. Keyed by the ids the removal destroyed
-- (Sonarr series, Radarr movie, Seerr request): a later new request gets new
-- ids and is not affected. Reaped after 30 days.
CREATE TABLE removed_media (
  id               INTEGER PRIMARY KEY,
  arr              TEXT,              -- sonarr|radarr (NULL for a seerr-only row)
  arr_id           INTEGER,           -- sonarr series id | radarr movie id
  seerr_request_id INTEGER,
  title            TEXT NOT NULL DEFAULT '',
  removed_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_removed_arr ON removed_media(arr, arr_id);
CREATE INDEX idx_removed_seerr ON removed_media(seerr_request_id);
