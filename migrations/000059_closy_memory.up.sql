CREATE TABLE IF NOT EXISTS closy_profiles (
    id                          UUID PRIMARY KEY,
    tenant_id                   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id                     VARCHAR(255) NOT NULL,
    agent_id                    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    style_summary               TEXT NOT NULL DEFAULT '',
    self_expression_summary     TEXT NOT NULL DEFAULT '',
    social_presentation_summary TEXT NOT NULL DEFAULT '',
    current_state_summary       TEXT NOT NULL DEFAULT '',
    confidence                  DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, user_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_closy_profiles_user_agent
    ON closy_profiles (tenant_id, user_id, agent_id);

CREATE TABLE IF NOT EXISTS closy_style_preferences (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id            VARCHAR(255) NOT NULL,
    agent_id           UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    category           VARCHAR(64) NOT NULL,
    polarity           VARCHAR(32) NOT NULL,
    value              TEXT NOT NULL,
    evidence           TEXT NOT NULL DEFAULT '',
    source_session_key TEXT NOT NULL DEFAULT '',
    confidence         DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, user_id, agent_id, category, polarity, value)
);

CREATE INDEX IF NOT EXISTS idx_closy_style_preferences_user_agent
    ON closy_style_preferences (tenant_id, user_id, agent_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_closy_style_preferences_category
    ON closy_style_preferences (tenant_id, user_id, agent_id, category, polarity);
