-- Keep claim scans online while scheduler_outbox may still be receiving writes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduler_outbox_claimable
    ON scheduler_outbox (next_attempt_at, lease_expires_at, id);
