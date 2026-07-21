-- One-time cleanup of duplicate orphan requests.
--
-- A re-grab or quality upgrade of an already-completed title used to match only
-- 'active' requests (resolveMovieItem/resolveEpisodeItem), so it spawned a fresh
-- orphan request (seerr_request_id NULL) every time — surfacing as duplicate
-- "Done" cards and duplicate completion notifications (e.g. one movie 6×).
-- Correlation now matches requests of any status (FindRequestByTmdb/Tvdb), so no
-- new orphans are created; remove the duplicates already accumulated.
--
-- Delete an orphan only when an EARLIER request for the same title exists (same
-- media_type + same tmdb_id or tvdb_id), keeping the original. foreign_keys is
-- ON, so the cascade drops each deleted request's media_items, stage_transitions
-- and download_items.
DELETE FROM requests
WHERE seerr_request_id IS NULL
  AND EXISTS (
    SELECT 1 FROM requests e
    WHERE e.id < requests.id AND e.media_type = requests.media_type
      AND ((requests.tmdb_id IS NOT NULL AND e.tmdb_id = requests.tmdb_id)
        OR (requests.tvdb_id IS NOT NULL AND e.tvdb_id = requests.tvdb_id)));
