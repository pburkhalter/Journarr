-- Request-level "waiting for release" for TV: unlike movies (which carry a
-- single media_item annotated with awaiting_release_at), a requested series has
-- no media_items until its episodes air. The TV waiting poller stamps the
-- request itself with the series' next-airing date so it shows in the Waiting
-- view alongside unreleased movies. NULL = not waiting.
ALTER TABLE requests ADD COLUMN awaiting_release_at DATETIME;
CREATE INDEX idx_requests_awaiting ON requests(awaiting_release_at)
  WHERE awaiting_release_at IS NOT NULL;
