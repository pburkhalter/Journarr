-- A hard download failure now starts a new attempt, because Sonarr/Radarr
-- blocklist the release and search again. Items left behind before that rule
-- (a pre-import stage whose every current-cycle download failed) get the
-- same reset as a Retry.
UPDATE media_items SET current_cycle = current_cycle + 1, current_stage = 'requested',
  last_error = '', stuck_since = NULL, updated_at = CURRENT_TIMESTAMP
WHERE current_stage IN ('grabbed', 'submitted', 'cloud_downloading', 'pulling', 'downloaded')
  AND request_id IN (SELECT id FROM requests WHERE status IN ('active', 'partial'))
  AND EXISTS (SELECT 1 FROM download_items di JOIN downloads d ON d.id = di.download_id
              WHERE di.media_item_id = media_items.id AND di.cycle = media_items.current_cycle
                AND d.state = 'failed')
  AND NOT EXISTS (SELECT 1 FROM download_items di JOIN downloads d ON d.id = di.download_id
                  WHERE di.media_item_id = media_items.id AND di.cycle = media_items.current_cycle
                    AND d.state <> 'failed');

-- An item past import has its file in the library; an error left on it by an
-- earlier attempt or a later failed upgrade is stale.
UPDATE media_items SET last_error = ''
WHERE COALESCE(last_error, '') <> ''
  AND current_stage IN (SELECT key FROM stages
                        WHERE ordinal >= (SELECT ordinal FROM stages WHERE key = 'imported'));

-- "Jellyfin scan on import" is gone: Sonarr and Radarr refresh Jellyfin
-- themselves on every import (their Jellyfin connection).
DELETE FROM flow_settings WHERE key = 'jellyfin_scan_on_import';
DELETE FROM flow_tasks WHERE kind = 'jellyfin_scan' AND status IN ('pending', 'failed');
