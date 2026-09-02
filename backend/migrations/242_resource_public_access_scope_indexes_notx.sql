-- Public ACL branches participate in every scoped account/group list. Keep
-- these online and partial so private or soft-deleted rows do not inflate the
-- index used by the public-access predicate.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_public_access_level
    ON accounts (public_access_level, id)
    WHERE public_access_level IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_public_access_level
    ON groups (public_access_level, id)
    WHERE public_access_level IS NOT NULL AND deleted_at IS NULL;
