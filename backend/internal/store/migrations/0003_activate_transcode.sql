-- Activate the transcode (Tdarr) stage. It only surfaces in the UI when a Tdarr
-- instance provides the transcode-stage capability (registry StageActive
-- gating), so flipping this on is inert until a Tdarr instance is configured.
UPDATE stages SET active = 1 WHERE key = 'transcode';
