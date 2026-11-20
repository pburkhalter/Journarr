-- Who triggered an intervention (OIDC user, api-token, mcp:<client>, flow,
-- system) and with which parameters (JSON), for the actions audit.
ALTER TABLE actions ADD COLUMN actor TEXT NOT NULL DEFAULT 'system';
ALTER TABLE actions ADD COLUMN params TEXT;
