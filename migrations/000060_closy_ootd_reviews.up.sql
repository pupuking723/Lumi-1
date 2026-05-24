CREATE TABLE IF NOT EXISTS closy_ootd_reviews (
    id                UUID PRIMARY KEY,
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           VARCHAR(255) NOT NULL,
    agent_id          UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    media_id          UUID NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    session_id        VARCHAR(255) NOT NULL DEFAULT '',
    occasion          TEXT NOT NULL DEFAULT '',
    user_note         TEXT NOT NULL DEFAULT '',
    overall_judgement TEXT NOT NULL DEFAULT '',
    style_label       TEXT NOT NULL DEFAULT '',
    highlight         TEXT NOT NULL DEFAULT '',
    main_issue        TEXT NOT NULL DEFAULT '',
    suggestion        TEXT NOT NULL DEFAULT '',
    mochi_line        TEXT NOT NULL DEFAULT '',
    safety_notes      TEXT NOT NULL DEFAULT '',
    raw_response      TEXT NOT NULL DEFAULT '',
    status            VARCHAR(32) NOT NULL DEFAULT 'completed',
    error_message     TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_closy_ootd_reviews_user
    ON closy_ootd_reviews (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_closy_ootd_reviews_agent
    ON closy_ootd_reviews (tenant_id, agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_closy_ootd_reviews_media
    ON closy_ootd_reviews (tenant_id, media_id);
