-- Dark-launch account/group ownership and typed ACL foundation.
-- Existing resources stay platform-owned, private in the new model, and in
-- legacy authorization mode. No application read path is switched here.

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS created_by_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS public_access_level VARCHAR(20),
    ADD COLUMN IF NOT EXISTS access_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS created_by_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS public_access_level VARCHAR(20),
    ADD COLUMN IF NOT EXISTS access_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS authorization_mode VARCHAR(20) NOT NULL DEFAULT 'legacy';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_owner_user_id_fkey'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_owner_user_id_fkey
            FOREIGN KEY (owner_user_id) REFERENCES users(id)
            ON DELETE RESTRICT NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_created_by_user_id_fkey'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_created_by_user_id_fkey
            FOREIGN KEY (created_by_user_id) REFERENCES users(id)
            ON DELETE RESTRICT NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_public_access_level_check'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_public_access_level_check
            CHECK (public_access_level IS NULL OR public_access_level IN ('viewer', 'consumer'))
            NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_access_version_positive'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_access_version_positive
            CHECK (access_version > 0) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_owner_user_id_fkey'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_owner_user_id_fkey
            FOREIGN KEY (owner_user_id) REFERENCES users(id)
            ON DELETE RESTRICT NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_created_by_user_id_fkey'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_created_by_user_id_fkey
            FOREIGN KEY (created_by_user_id) REFERENCES users(id)
            ON DELETE RESTRICT NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_public_access_level_check'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_public_access_level_check
            CHECK (public_access_level IS NULL OR public_access_level IN ('viewer', 'consumer'))
            NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_access_version_positive'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_access_version_positive
            CHECK (access_version > 0) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_authorization_mode_check'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_authorization_mode_check
            CHECK (authorization_mode IN ('legacy', 'shadow', 'acl')) NOT VALID;
    END IF;
END
$$;

-- All legacy owner/creator/public values are NULL and the version/mode columns
-- use safe constants, so none of these constraints should remain NOT VALID.
ALTER TABLE accounts
    VALIDATE CONSTRAINT accounts_owner_user_id_fkey,
    VALIDATE CONSTRAINT accounts_created_by_user_id_fkey,
    VALIDATE CONSTRAINT accounts_public_access_level_check,
    VALIDATE CONSTRAINT accounts_access_version_positive;

ALTER TABLE groups
    VALIDATE CONSTRAINT groups_owner_user_id_fkey,
    VALIDATE CONSTRAINT groups_created_by_user_id_fkey,
    VALIDATE CONSTRAINT groups_public_access_level_check,
    VALIDATE CONSTRAINT groups_access_version_positive,
    VALIDATE CONSTRAINT groups_authorization_mode_check;

CREATE TABLE IF NOT EXISTS account_access_grants (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    grantee_user_id BIGINT,
    grantee_role_id BIGINT,
    access_level VARCHAR(20) NOT NULL,
    granted_by_user_id BIGINT,
    granted_by_service_principal_id BIGINT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_access_grants_account_id_fkey
        FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT account_access_grants_grantee_user_id_fkey
        FOREIGN KEY (grantee_user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT account_access_grants_grantee_role_id_fkey
        FOREIGN KEY (grantee_role_id) REFERENCES roles(id) ON DELETE RESTRICT,
    CONSTRAINT account_access_grants_granted_by_user_id_fkey
        FOREIGN KEY (granted_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT account_access_grants_granted_by_service_principal_id_fkey
        FOREIGN KEY (granted_by_service_principal_id)
        REFERENCES service_principals(id) ON DELETE RESTRICT,
    CONSTRAINT account_access_grants_grantee_exactly_one_check
        CHECK (num_nonnulls(grantee_user_id, grantee_role_id) = 1),
    CONSTRAINT account_access_grants_grantor_exactly_one_check
        CHECK (num_nonnulls(granted_by_user_id, granted_by_service_principal_id) = 1),
    CONSTRAINT account_access_grants_access_level_check
        CHECK (access_level IN ('viewer', 'consumer', 'maintainer', 'manager'))
);

CREATE TABLE IF NOT EXISTS group_access_grants (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    grantee_user_id BIGINT,
    grantee_role_id BIGINT,
    access_level VARCHAR(20) NOT NULL,
    granted_by_user_id BIGINT,
    granted_by_service_principal_id BIGINT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT group_access_grants_group_id_fkey
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT group_access_grants_grantee_user_id_fkey
        FOREIGN KEY (grantee_user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT group_access_grants_grantee_role_id_fkey
        FOREIGN KEY (grantee_role_id) REFERENCES roles(id) ON DELETE RESTRICT,
    CONSTRAINT group_access_grants_granted_by_user_id_fkey
        FOREIGN KEY (granted_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT group_access_grants_granted_by_service_principal_id_fkey
        FOREIGN KEY (granted_by_service_principal_id)
        REFERENCES service_principals(id) ON DELETE RESTRICT,
    CONSTRAINT group_access_grants_grantee_exactly_one_check
        CHECK (num_nonnulls(grantee_user_id, grantee_role_id) = 1),
    CONSTRAINT group_access_grants_grantor_exactly_one_check
        CHECK (num_nonnulls(granted_by_user_id, granted_by_service_principal_id) = 1),
    CONSTRAINT group_access_grants_access_level_check
        CHECK (access_level IN ('viewer', 'consumer', 'maintainer', 'manager'))
);

CREATE UNIQUE INDEX IF NOT EXISTS account_access_grants_account_user_key
    ON account_access_grants (account_id, grantee_user_id)
    WHERE grantee_user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS account_access_grants_account_role_key
    ON account_access_grants (account_id, grantee_role_id)
    WHERE grantee_role_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_account_access_grants_grantee_user
    ON account_access_grants (grantee_user_id, account_id)
    WHERE grantee_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_account_access_grants_grantee_role
    ON account_access_grants (grantee_role_id, account_id)
    WHERE grantee_role_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_account_access_grants_expires_at
    ON account_access_grants (expires_at, id)
    WHERE expires_at IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS group_access_grants_group_user_key
    ON group_access_grants (group_id, grantee_user_id)
    WHERE grantee_user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS group_access_grants_group_role_key
    ON group_access_grants (group_id, grantee_role_id)
    WHERE grantee_role_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_group_access_grants_grantee_user
    ON group_access_grants (grantee_user_id, group_id)
    WHERE grantee_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_group_access_grants_grantee_role
    ON group_access_grants (grantee_role_id, group_id)
    WHERE grantee_role_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_group_access_grants_expires_at
    ON group_access_grants (expires_at, id)
    WHERE expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS resource_authorization_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT,
    group_id BIGINT,
    resource_owner_user_id BIGINT,
    actor_user_id BIGINT,
    actor_service_principal_id BIGINT,
    auth_method VARCHAR(32) NOT NULL DEFAULT 'unknown',
    event_type VARCHAR(64) NOT NULL,
    resource_access_version BIGINT NOT NULL,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT resource_authorization_events_account_id_fkey
        FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT,
    CONSTRAINT resource_authorization_events_group_id_fkey
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE RESTRICT,
    CONSTRAINT resource_authorization_events_resource_owner_user_id_fkey
        FOREIGN KEY (resource_owner_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT resource_authorization_events_actor_user_id_fkey
        FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT resource_authorization_events_actor_service_principal_id_fkey
        FOREIGN KEY (actor_service_principal_id)
        REFERENCES service_principals(id) ON DELETE RESTRICT,
    CONSTRAINT resource_authorization_events_resource_exactly_one_check
        CHECK (num_nonnulls(account_id, group_id) = 1),
    CONSTRAINT resource_authorization_events_actor_exactly_one_check
        CHECK (num_nonnulls(actor_user_id, actor_service_principal_id) = 1),
    CONSTRAINT resource_authorization_events_auth_method_not_empty_check
        CHECK (BTRIM(auth_method) <> ''),
    CONSTRAINT resource_authorization_events_event_type_not_empty_check
        CHECK (BTRIM(event_type) <> ''),
    CONSTRAINT resource_authorization_events_access_version_positive
        CHECK (resource_access_version > 0),
    CONSTRAINT resource_authorization_events_details_object_check
        CHECK (jsonb_typeof(details) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_resource_authorization_events_account_created
    ON resource_authorization_events (account_id, created_at DESC, id DESC)
    WHERE account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_resource_authorization_events_group_created
    ON resource_authorization_events (group_id, created_at DESC, id DESC)
    WHERE group_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_resource_authorization_events_actor_user_created
    ON resource_authorization_events (actor_user_id, created_at DESC, id DESC)
    WHERE actor_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_resource_authorization_events_actor_sp_created
    ON resource_authorization_events (actor_service_principal_id, created_at DESC, id DESC)
    WHERE actor_service_principal_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_resource_authorization_events_created_at
    ON resource_authorization_events (created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION reject_resource_authorization_event_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'resource_authorization_events is append-only'
        USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS resource_authorization_events_immutable
    ON resource_authorization_events;
CREATE TRIGGER resource_authorization_events_immutable
BEFORE UPDATE OR DELETE OR TRUNCATE ON resource_authorization_events
FOR EACH STATEMENT
EXECUTE FUNCTION reject_resource_authorization_event_mutation();
