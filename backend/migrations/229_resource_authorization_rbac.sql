-- Dark-launch platform authorization foundation.
-- The legacy users.role column remains authoritative until the application
-- explicitly advances role_authorization_mode out of legacy.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS authz_version BIGINT NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'users_authz_version_positive'
          AND conrelid = 'users'::regclass
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT users_authz_version_positive
            CHECK (authz_version > 0) NOT VALID;
    END IF;
END
$$;

-- The new column is populated by its constant default for every legacy row,
-- so the constraint can be fully validated in the same release.
ALTER TABLE users
    VALIDATE CONSTRAINT users_authz_version_positive;

CREATE TABLE IF NOT EXISTS roles (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    authz_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT roles_code_key UNIQUE (code),
    CONSTRAINT roles_authz_version_positive CHECK (authz_version > 0)
);

CREATE TABLE IF NOT EXISTS permissions (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT permissions_code_key UNIQUE (code)
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id BIGINT NOT NULL,
    permission_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT role_permissions_pkey PRIMARY KEY (role_id, permission_id),
    CONSTRAINT role_permissions_role_id_fkey
        FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE,
    CONSTRAINT role_permissions_permission_id_fkey
        FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS service_principals (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    authz_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT service_principals_code_key UNIQUE (code),
    CONSTRAINT service_principals_status_check
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT service_principals_authz_version_positive
        CHECK (authz_version > 0)
);

CREATE TABLE IF NOT EXISTS user_roles (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    granted_by_user_id BIGINT,
    granted_by_service_principal_id BIGINT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_roles_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT user_roles_role_id_fkey
        FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT,
    CONSTRAINT user_roles_granted_by_user_id_fkey
        FOREIGN KEY (granted_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT user_roles_granted_by_service_principal_id_fkey
        FOREIGN KEY (granted_by_service_principal_id)
        REFERENCES service_principals(id) ON DELETE RESTRICT,
    CONSTRAINT user_roles_grantor_exactly_one_check
        CHECK (num_nonnulls(granted_by_user_id, granted_by_service_principal_id) = 1),
    CONSTRAINT user_roles_user_role_key UNIQUE (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS service_principal_roles (
    id BIGSERIAL PRIMARY KEY,
    service_principal_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    granted_by_user_id BIGINT NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT service_principal_roles_service_principal_id_fkey
        FOREIGN KEY (service_principal_id)
        REFERENCES service_principals(id) ON DELETE CASCADE,
    CONSTRAINT service_principal_roles_role_id_fkey
        FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT,
    CONSTRAINT service_principal_roles_granted_by_user_id_fkey
        FOREIGN KEY (granted_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT service_principal_roles_principal_role_key
        UNIQUE (service_principal_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_role_id
    ON user_roles (role_id, user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_expires_at
    ON user_roles (expires_at, id)
    WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_service_principal_roles_role_id
    ON service_principal_roles (role_id, service_principal_id);
CREATE INDEX IF NOT EXISTS idx_service_principal_roles_expires_at
    ON service_principal_roles (expires_at, id)
    WHERE expires_at IS NOT NULL;

INSERT INTO permissions (code, description)
VALUES
    ('api_key.create', 'Create API keys owned by the current user'),
    ('account.create', 'Create upstream accounts owned by the current user'),
    ('group.create', 'Create groups owned by the current user'),
    ('resource.share', 'Share resources whose access the actor may manage'),
    ('resource.transfer', 'Transfer resources owned by the current user'),
    ('platform.resource.view_all', 'View the safe projection of every resource'),
    ('platform.resource.operate_all', 'Perform platform operations on every account'),
    ('platform.resource.manage_all', 'Manage every platform resource'),
    ('platform.role.manage', 'Manage roles and role permissions'),
    ('platform.grant.manage', 'Grant or revoke resource access for the platform'),
    ('platform.secret.export', 'Perform audited break-glass secret export')
ON CONFLICT (code) DO UPDATE
SET description = EXCLUDED.description;

INSERT INTO roles (code, name, description, is_system)
VALUES
    ('user', 'User', 'Base consumer role', TRUE),
    ('hoster', 'Hoster', 'May host private accounts and groups', TRUE),
    ('platform_operator', 'Platform Operator', 'May view and operate platform resources', TRUE),
    ('admin', 'Administrator', 'Platform governance role', TRUE)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name,
    description = EXCLUDED.description,
    is_system = TRUE;

-- This principal records provenance for migration-created compatibility roles.
-- It has no credential and receives no role or permission.
INSERT INTO service_principals (code, name, status)
VALUES ('system_bootstrap', 'System Bootstrap', 'active')
ON CONFLICT (code) DO NOTHING;

WITH desired_role_permissions(role_code, permission_code) AS (
    VALUES
        ('user', 'api_key.create'),
        ('hoster', 'api_key.create'),
        ('hoster', 'account.create'),
        ('hoster', 'group.create'),
        ('hoster', 'resource.share'),
        ('platform_operator', 'platform.resource.view_all'),
        ('platform_operator', 'platform.resource.operate_all'),
        ('admin', 'api_key.create'),
        ('admin', 'account.create'),
        ('admin', 'group.create'),
        ('admin', 'resource.share'),
        ('admin', 'resource.transfer'),
        ('admin', 'platform.resource.view_all'),
        ('admin', 'platform.resource.operate_all'),
        ('admin', 'platform.resource.manage_all'),
        ('admin', 'platform.role.manage'),
        ('admin', 'platform.grant.manage')
)
INSERT INTO role_permissions (role_id, permission_id)
SELECT roles.id, permissions.id
FROM desired_role_permissions
JOIN roles ON roles.code = desired_role_permissions.role_code
JOIN permissions ON permissions.code = desired_role_permissions.permission_code
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Preserve legacy behavior: users.role remains the sole authorization source.
-- Unknown legacy role strings receive the base compatibility role and remain
-- governed by the unchanged legacy path until data preflight resolves them.
-- On retry, converge only migration-owned user/admin compatibility grants to
-- the current legacy role. Grants made by people, other service principals,
-- and system_bootstrap grants for any other role are intentionally preserved.
DELETE FROM user_roles
USING users, roles, service_principals
WHERE user_roles.user_id = users.id
  AND user_roles.role_id = roles.id
  AND user_roles.granted_by_service_principal_id = service_principals.id
  AND service_principals.code = 'system_bootstrap'
  AND roles.code IN ('user', 'admin')
  AND roles.code <> CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END;

INSERT INTO user_roles (
    user_id,
    role_id,
    granted_by_service_principal_id
)
SELECT
    users.id,
    roles.id,
    service_principals.id
FROM users
JOIN roles
    ON roles.code = CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END
JOIN service_principals
    ON service_principals.code = 'system_bootstrap'
ON CONFLICT (user_id, role_id) DO NOTHING;
