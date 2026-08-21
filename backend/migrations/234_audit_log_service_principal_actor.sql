-- Attribute machine-authenticated management requests to their durable
-- Service Principal instead of impersonating the first administrator.

-- This built-in principal is an identity anchor only. It deliberately has no
-- role assignment; legacy admin API-key authorization remains unchanged while
-- runtime RBAC stays dark-launched.
INSERT INTO service_principals (code, name, status)
VALUES ('admin_api_key', 'Admin API Key', 'active')
ON CONFLICT (code) DO NOTHING;

-- The reserved admin API-key identity is an audit provenance anchor, not an
-- RBAC principal. Clear any role left by a pre-migration code collision while
-- preserving the existing principal status (including deliberately disabled).
DELETE FROM service_principal_roles
WHERE service_principal_id = (
    SELECT id
    FROM service_principals
    WHERE code = 'admin_api_key'
);

ALTER TABLE audit_logs
    ADD COLUMN IF NOT EXISTS actor_service_principal_id BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'audit_logs_actor_service_principal_id_fkey'
          AND conrelid = 'audit_logs'::regclass
    ) THEN
        ALTER TABLE audit_logs
            ADD CONSTRAINT audit_logs_actor_service_principal_id_fkey
            FOREIGN KEY (actor_service_principal_id)
            REFERENCES service_principals(id)
            ON DELETE RESTRICT
            NOT VALID;
    END IF;
END
$$;

ALTER TABLE audit_logs
    VALIDATE CONSTRAINT audit_logs_actor_service_principal_id_fkey;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'audit_logs_actor_at_most_one_check'
          AND conrelid = 'audit_logs'::regclass
    ) THEN
        ALTER TABLE audit_logs
            ADD CONSTRAINT audit_logs_actor_at_most_one_check
            CHECK (num_nonnulls(actor_user_id, actor_service_principal_id) <= 1)
            NOT VALID;
    END IF;
END
$$;

-- Actor-less authentication failures are valid historical events, hence this
-- is an at-most-one constraint rather than an exactly-one constraint.
ALTER TABLE audit_logs
    VALIDATE CONSTRAINT audit_logs_actor_at_most_one_check;
