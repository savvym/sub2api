ALTER TABLE scheduler_outbox
    ADD COLUMN IF NOT EXISTS lease_token VARCHAR(64),
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    ADD COLUMN IF NOT EXISTS attempt_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error VARCHAR(1024);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'scheduler_outbox_attempt_count_nonnegative'
          AND conrelid = 'scheduler_outbox'::regclass
    ) THEN
        ALTER TABLE scheduler_outbox
            ADD CONSTRAINT scheduler_outbox_attempt_count_nonnegative
            CHECK (attempt_count >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'scheduler_outbox_lease_pair_consistent'
          AND conrelid = 'scheduler_outbox'::regclass
    ) THEN
        ALTER TABLE scheduler_outbox
            ADD CONSTRAINT scheduler_outbox_lease_pair_consistent
            CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)) NOT VALID;
    END IF;
END $$;
