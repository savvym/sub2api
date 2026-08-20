-- Build indexes on the existing accounts/groups tables without blocking writes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_owner_user_id
    ON accounts (owner_user_id)
    WHERE owner_user_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_created_by_user_id
    ON accounts (created_by_user_id)
    WHERE created_by_user_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_owner_user_id
    ON groups (owner_user_id)
    WHERE owner_user_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_created_by_user_id
    ON groups (created_by_user_id)
    WHERE created_by_user_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_authorization_mode
    ON groups (authorization_mode, id);
