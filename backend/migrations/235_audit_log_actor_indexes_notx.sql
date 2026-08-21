-- Replace the original non-partial user index and add the equivalent Service
-- Principal lookup path without blocking audit writes during a rolling
-- deployment. Dropping first also clears an invalid concurrent index left by
-- an interrupted previous attempt before IF NOT EXISTS is evaluated.
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_actor_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_service_principal_created;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_actor_created
    ON audit_logs (actor_user_id, created_at DESC)
    WHERE actor_user_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_service_principal_created
    ON audit_logs (actor_service_principal_id, created_at DESC)
    WHERE actor_service_principal_id IS NOT NULL;
