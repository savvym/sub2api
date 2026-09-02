CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_platform_name_unique_active
    ON groups (lower(name))
    WHERE owner_user_id IS NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_owner_name_unique_active
    ON groups (owner_user_id, lower(name))
    WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL;

DROP INDEX CONCURRENTLY IF EXISTS groups_name_unique_active;
