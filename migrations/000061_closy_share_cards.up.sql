CREATE TABLE IF NOT EXISTS closy_share_cards (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         VARCHAR(255) NOT NULL,
    agent_id        UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    ootd_review_id  UUID NOT NULL REFERENCES closy_ootd_reviews(id) ON DELETE CASCADE,
    media_id        UUID NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    slug            VARCHAR(64) NOT NULL UNIQUE,
    share_url       TEXT NOT NULL DEFAULT '',
    cta_text        TEXT NOT NULL DEFAULT '',
    cta_url         TEXT NOT NULL DEFAULT '',
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    status          VARCHAR(32) NOT NULL DEFAULT 'active',
    view_count      BIGINT NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_closy_share_cards_user
    ON closy_share_cards (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_closy_share_cards_review
    ON closy_share_cards (tenant_id, ootd_review_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_closy_share_cards_slug
    ON closy_share_cards (slug);
