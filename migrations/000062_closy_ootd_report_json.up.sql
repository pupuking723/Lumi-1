ALTER TABLE closy_ootd_reviews
    ADD COLUMN IF NOT EXISTS report_json JSONB;
