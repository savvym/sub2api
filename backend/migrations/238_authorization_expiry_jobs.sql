-- Durable coordination for authorization sources whose effect ends at a
-- database timestamp. Runtime policy evaluation still rejects expired rows
-- synchronously; these jobs converge versions, caches, audit, and scheduler
-- state across instances after that boundary.

-- This built-in principal is an audit identity only. It has no credential and
-- must never gain authorization through a role, including when its reserved
-- code collided with pre-existing data.
INSERT INTO service_principals (code, name, status)
VALUES (
    'authorization_expiry_coordinator',
    'Authorization Expiry Coordinator',
    'active'
)
ON CONFLICT (code) DO NOTHING;

-- Preserve an existing disabled status on collision while restoring the
-- identity-only invariant.
DELETE FROM service_principal_roles
WHERE service_principal_id = (
    SELECT id
    FROM service_principals
    WHERE code = 'authorization_expiry_coordinator'
);

CREATE TABLE IF NOT EXISTS authorization_expiry_jobs (
    id BIGSERIAL PRIMARY KEY,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ,
    claimed_by TEXT,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT authorization_expiry_jobs_source_type_check
        CHECK (source_type IN (
            'user_role',
            'service_principal_role',
            'account_access_grant',
            'group_access_grant'
        )),
    CONSTRAINT authorization_expiry_jobs_source_id_positive
        CHECK (source_id > 0),
    CONSTRAINT authorization_expiry_jobs_attempts_nonnegative
        CHECK (attempts >= 0),
    CONSTRAINT authorization_expiry_jobs_available_after_expiry
        CHECK (available_at >= expires_at),
    CONSTRAINT authorization_expiry_jobs_claim_pair_check
        CHECK ((claimed_at IS NULL) = (claimed_by IS NULL)),
    CONSTRAINT authorization_expiry_jobs_source_key
        UNIQUE (source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_due
    ON authorization_expiry_jobs (available_at, id)
    WHERE processed_at IS NULL AND claimed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_lease
    ON authorization_expiry_jobs (claimed_at, id)
    WHERE processed_at IS NULL AND claimed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_lag
    ON authorization_expiry_jobs (expires_at, id)
    WHERE processed_at IS NULL;

CREATE OR REPLACE FUNCTION sync_authorization_expiry_job()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    source_kind VARCHAR(40);
BEGIN
    source_kind := TG_ARGV[0];
    IF source_kind NOT IN (
        'user_role',
        'service_principal_role',
        'account_access_grant',
        'group_access_grant'
    ) THEN
        RAISE EXCEPTION 'unsupported authorization expiry source type: %', source_kind;
    END IF;

    IF TG_OP = 'DELETE' THEN
        DELETE FROM authorization_expiry_jobs
        WHERE source_type = source_kind
          AND source_id = OLD.id;
        RETURN OLD;
    END IF;

    IF NEW.expires_at IS NULL THEN
        DELETE FROM authorization_expiry_jobs
        WHERE source_type = source_kind
          AND source_id = NEW.id;
        RETURN NEW;
    END IF;

    INSERT INTO authorization_expiry_jobs (
        source_type,
        source_id,
        expires_at,
        available_at
    )
    VALUES (
        source_kind,
        NEW.id,
        NEW.expires_at,
        NEW.expires_at
    )
    ON CONFLICT (source_type, source_id) DO UPDATE
    SET expires_at = EXCLUDED.expires_at,
        available_at = EXCLUDED.available_at,
        attempts = 0,
        last_error = '',
        claimed_at = NULL,
        claimed_by = NULL,
        processed_at = NULL,
        updated_at = statement_timestamp()
    -- Replaying the migration or writing an unrelated source column must not
    -- resurrect an already processed job or erase retry/lease state.
    WHERE authorization_expiry_jobs.expires_at
        IS DISTINCT FROM EXCLUDED.expires_at;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_roles_authorization_expiry ON user_roles;
CREATE TRIGGER trg_user_roles_authorization_expiry
AFTER INSERT OR DELETE OR UPDATE OF expires_at ON user_roles
FOR EACH ROW
EXECUTE FUNCTION sync_authorization_expiry_job('user_role');

DROP TRIGGER IF EXISTS trg_service_principal_roles_authorization_expiry
    ON service_principal_roles;
CREATE TRIGGER trg_service_principal_roles_authorization_expiry
AFTER INSERT OR DELETE OR UPDATE OF expires_at ON service_principal_roles
FOR EACH ROW
EXECUTE FUNCTION sync_authorization_expiry_job('service_principal_role');

DROP TRIGGER IF EXISTS trg_account_access_grants_authorization_expiry
    ON account_access_grants;
CREATE TRIGGER trg_account_access_grants_authorization_expiry
AFTER INSERT OR DELETE OR UPDATE OF expires_at ON account_access_grants
FOR EACH ROW
EXECUTE FUNCTION sync_authorization_expiry_job('account_access_grant');

DROP TRIGGER IF EXISTS trg_group_access_grants_authorization_expiry
    ON group_access_grants;
CREATE TRIGGER trg_group_access_grants_authorization_expiry
AFTER INSERT OR DELETE OR UPDATE OF expires_at ON group_access_grants
FOR EACH ROW
EXECUTE FUNCTION sync_authorization_expiry_job('group_access_grant');

-- Backfill both future and already-due sources. The conflict predicate is
-- deliberately identical to the trigger: a matching processed job stays
-- processed on migration reapplication, while an extended or shortened
-- expiry is rearmed.
INSERT INTO authorization_expiry_jobs (
    source_type,
    source_id,
    expires_at,
    available_at
)
SELECT
    expiring_sources.source_type,
    expiring_sources.source_id,
    expiring_sources.expires_at,
    expiring_sources.expires_at
FROM (
    SELECT
        'user_role'::VARCHAR(40) AS source_type,
        id AS source_id,
        expires_at
    FROM user_roles
    WHERE expires_at IS NOT NULL

    UNION ALL

    SELECT
        'service_principal_role'::VARCHAR(40),
        id,
        expires_at
    FROM service_principal_roles
    WHERE expires_at IS NOT NULL

    UNION ALL

    SELECT
        'account_access_grant'::VARCHAR(40),
        id,
        expires_at
    FROM account_access_grants
    WHERE expires_at IS NOT NULL

    UNION ALL

    SELECT
        'group_access_grant'::VARCHAR(40),
        id,
        expires_at
    FROM group_access_grants
    WHERE expires_at IS NOT NULL
) AS expiring_sources
ON CONFLICT (source_type, source_id) DO UPDATE
SET expires_at = EXCLUDED.expires_at,
    available_at = EXCLUDED.available_at,
    attempts = 0,
    last_error = '',
    claimed_at = NULL,
    claimed_by = NULL,
    processed_at = NULL,
    updated_at = statement_timestamp()
WHERE authorization_expiry_jobs.expires_at
    IS DISTINCT FROM EXCLUDED.expires_at;

COMMENT ON TABLE authorization_expiry_jobs IS
    'Durable work queue for converging authorization state at source expiry';
