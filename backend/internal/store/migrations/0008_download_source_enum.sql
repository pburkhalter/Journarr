-- The arr history API reports the download protocol as an enum string ("1"
-- usenet, "2" torrent). The history poller stored it verbatim until 2026-09,
-- so almost every download carried source '1'. Map the legacy rows; new rows
-- are normalized on write.
UPDATE downloads SET source = 'usenet' WHERE source = '1';
UPDATE downloads SET source = 'torrent' WHERE source = '2';
