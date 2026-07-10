-- Movies requested before their digital/physical release sit at 'approved'
-- forever — Radarr can't grab a film that isn't out yet, so they never advance
-- and would eventually be mis-flagged as stuck. awaiting_release_at annotates
-- such an item with the expected availability date: the UI shows "waiting for
-- release" instead of a stall, and the stuck sweeper skips it. NULL = not
-- awaiting; a timestamp = Radarr's expected release date.
ALTER TABLE media_items ADD COLUMN awaiting_release_at DATETIME;
CREATE INDEX idx_media_items_awaiting ON media_items(awaiting_release_at)
  WHERE awaiting_release_at IS NOT NULL;
