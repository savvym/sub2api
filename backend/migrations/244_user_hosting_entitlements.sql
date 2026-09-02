-- Per-user private hosting quota configuration. The hoster qualification
-- remains authoritative in user_roles; this table stores only quota state and
-- the compare-and-swap version used by the guarded administrator endpoint.

CREATE TABLE IF NOT EXISTS user_hosting_entitlements (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    account_limit BIGINT NOT NULL DEFAULT 0,
    group_limit BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    created_by_user_id BIGINT NOT NULL,
    updated_by_user_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_hosting_entitlements_user_id_key UNIQUE (user_id),
    CONSTRAINT user_hosting_entitlements_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT user_hosting_entitlements_created_by_user_id_fkey
        FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT user_hosting_entitlements_updated_by_user_id_fkey
        FOREIGN KEY (updated_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT user_hosting_entitlements_account_limit_nonnegative
        CHECK (account_limit >= 0),
    CONSTRAINT user_hosting_entitlements_group_limit_nonnegative
        CHECK (group_limit >= 0),
    CONSTRAINT user_hosting_entitlements_version_positive
        CHECK (version > 0)
);
