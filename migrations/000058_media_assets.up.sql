CREATE TABLE IF NOT EXISTS media_assets (
    id                UUID PRIMARY KEY,
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           VARCHAR(255) NOT NULL DEFAULT '',
    session_id        VARCHAR(255),
    agent_id          UUID REFERENCES agents(id) ON DELETE SET NULL,
    original_filename TEXT NOT NULL,
    mime_type         TEXT NOT NULL,
    file_size         BIGINT NOT NULL DEFAULT 0,
    sha256            TEXT NOT NULL DEFAULT '',
    storage_backend   VARCHAR(32) NOT NULL DEFAULT 'local',
    storage_bucket    TEXT,
    storage_key       TEXT NOT NULL,
    status            VARCHAR(32) NOT NULL DEFAULT 'ready',
    visibility        VARCHAR(32) NOT NULL DEFAULT 'private',
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_assets_tenant_user
    ON media_assets (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_media_assets_session
    ON media_assets (tenant_id, session_id)
    WHERE session_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_media_assets_agent
    ON media_assets (tenant_id, agent_id)
    WHERE agent_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_media_assets_sha256
    ON media_assets (tenant_id, sha256)
    WHERE sha256 <> '';
