-- Keep the primary invalidation pass ahead of delayed safety-pass work without
-- taking a blocking index-build lock on this production outbox.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_auth_cache_invalidation_outbox_stage_available
    ON auth_cache_invalidation_outbox (delivery_stage, available_at, id);
