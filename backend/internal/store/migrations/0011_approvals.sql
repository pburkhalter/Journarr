-- Removal requests filed by MCP clients. Nothing is deleted until the owner
-- approves one in the UI with an SSO session; the plan reviewed then must
-- still have the stored plan_hash.
CREATE TABLE approvals (
  id           INTEGER PRIMARY KEY,
  kind         TEXT NOT NULL DEFAULT 'remove' CHECK(kind IN ('remove')),
  target       TEXT NOT NULL,                 -- JSON actions.Title
  target_key   TEXT NOT NULL,                 -- one pending request per title
  title        TEXT NOT NULL,
  plan         TEXT NOT NULL,                 -- JSON RemovalPlan when requested
  plan_hash    TEXT NOT NULL,
  reason       TEXT NOT NULL DEFAULT '',
  requested_by TEXT NOT NULL,                 -- mcp:<client>
  status       TEXT NOT NULL DEFAULT 'pending'
               CHECK(status IN ('pending','approved','rejected','expired','executed','failed')),
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   DATETIME NOT NULL,
  decided_at   DATETIME,
  decided_by   TEXT,
  result       TEXT                            -- JSON {error, steps}
);
CREATE INDEX approvals_status_created ON approvals(status, created_at);
CREATE UNIQUE INDEX approvals_one_pending ON approvals(target_key) WHERE status = 'pending';
