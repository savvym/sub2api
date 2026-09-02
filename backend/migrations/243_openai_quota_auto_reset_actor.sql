-- Give the OpenAI quota auto-reset worker a durable, revocable identity without
-- attaching a normal RBAC role. Normal Service Principal roles participate in
-- role_authorization_mode and would make shadow -> legacy rollback impossible;
-- this direct worker permission is consumed only by the dedicated worker policy.

CREATE TABLE IF NOT EXISTS service_principal_worker_permissions (
    service_principal_id BIGINT NOT NULL,
    permission_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT service_principal_worker_permissions_pkey
        PRIMARY KEY (service_principal_id, permission_id),
    CONSTRAINT sp_worker_permissions_principal_id_fkey
        FOREIGN KEY (service_principal_id)
        REFERENCES service_principals(id) ON DELETE CASCADE,
    CONSTRAINT sp_worker_permissions_permission_id_fkey
        FOREIGN KEY (permission_id)
        REFERENCES permissions(id) ON DELETE CASCADE
);

-- CREATE TABLE IF NOT EXISTS must not accept a look-alike table. The worker
-- policy relies on this being a narrow many-to-many relation with cascading
-- ownership; fail before seeding any privileged identity when it is not exact.
DO $$
DECLARE
    relation_id OID := to_regclass('service_principal_worker_permissions');
    columns_are_exact BOOLEAN;
    constraints_are_exact BOOLEAN;
    primary_key_is_exact BOOLEAN;
    principal_fk_is_exact BOOLEAN;
    permission_fk_is_exact BOOLEAN;
BEGIN
    IF relation_id IS NULL THEN
        RAISE EXCEPTION 'service_principal_worker_permissions was not created';
    END IF;

    SELECT
        COUNT(*) = 3
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'service_principal_id'
              AND attribute.atttypid = 'bigint'::regtype
              AND attribute.attnotnull
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'permission_id'
              AND attribute.atttypid = 'bigint'::regtype
              AND attribute.attnotnull
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'created_at'
              AND attribute.atttypid = 'timestamptz'::regtype
              AND attribute.attnotnull
              AND pg_get_expr(default_value.adbin, default_value.adrelid) = 'now()'
        ) = 1
    INTO columns_are_exact
    FROM pg_attribute AS attribute
    LEFT JOIN pg_attrdef AS default_value
        ON default_value.adrelid = attribute.attrelid
       AND default_value.adnum = attribute.attnum
    WHERE attribute.attrelid = relation_id
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped;

    SELECT
        COUNT(*) FILTER (
            WHERE constraint_row.contype IN ('p', 'f', 'u', 'c', 'x')
        ) = 3
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'p') = 1
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'f') = 2
    INTO constraints_are_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = relation_id;

    SELECT COUNT(*) = 1
    INTO primary_key_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = relation_id
      AND constraint_row.conname = 'service_principal_worker_permissions_pkey'
      AND constraint_row.contype = 'p'
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = relation_id
                AND attribute.attname = 'service_principal_id'
                AND NOT attribute.attisdropped
          ),
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = relation_id
                AND attribute.attname = 'permission_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    SELECT COUNT(*) = 1
    INTO principal_fk_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = relation_id
      AND constraint_row.conname = 'sp_worker_permissions_principal_id_fkey'
      AND constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'service_principals'::regclass
      AND constraint_row.confdeltype = 'c'
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = relation_id
                AND attribute.attname = 'service_principal_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND constraint_row.confkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = 'service_principals'::regclass
                AND attribute.attname = 'id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    SELECT COUNT(*) = 1
    INTO permission_fk_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = relation_id
      AND constraint_row.conname = 'sp_worker_permissions_permission_id_fkey'
      AND constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'permissions'::regclass
      AND constraint_row.confdeltype = 'c'
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = relation_id
                AND attribute.attname = 'permission_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND constraint_row.confkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = 'permissions'::regclass
                AND attribute.attname = 'id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    IF NOT COALESCE(columns_are_exact, FALSE)
       OR NOT COALESCE(constraints_are_exact, FALSE)
       OR NOT COALESCE(primary_key_is_exact, FALSE)
       OR NOT COALESCE(principal_fk_is_exact, FALSE)
       OR NOT COALESCE(permission_fk_is_exact, FALSE) THEN
        RAISE EXCEPTION
            'unsafe existing service_principal_worker_permissions table shape';
    END IF;
END
$$;

-- A stable upstream redemption request identifies exactly one successful
-- auto-reset audit. Empty request IDs belong to historical binaries and stay
-- outside the constraint so this migration never invents their identity.
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_logs_openai_auto_reset_request_id
    ON audit_logs (action, request_id)
    WHERE action = 'system.openai.reset_credit.auto'
      AND request_id <> '';

-- IF NOT EXISTS must not accept a same-name look-alike index. The repository
-- uses this exact partial unique index as its ON CONFLICT arbiter.
DO $$
DECLARE
    index_id OID := to_regclass('idx_audit_logs_openai_auto_reset_request_id');
    index_is_exact BOOLEAN;
BEGIN
    IF index_id IS NULL THEN
        RAISE EXCEPTION 'idx_audit_logs_openai_auto_reset_request_id was not created';
    END IF;

    SELECT COUNT(*) = 1
    INTO index_is_exact
    FROM pg_index AS index_row
    JOIN pg_class AS index_relation
      ON index_relation.oid = index_row.indexrelid
    JOIN pg_am AS access_method
      ON access_method.oid = index_relation.relam
    WHERE index_row.indexrelid = index_id
      AND index_row.indrelid = 'audit_logs'::regclass
      AND index_row.indisunique
      AND index_row.indisvalid
      AND index_row.indisready
      AND index_row.indimmediate
      AND NOT index_row.indisexclusion
      AND index_row.indexprs IS NULL
      AND index_row.indnkeyatts = 2
      AND index_row.indnatts = 2
      AND access_method.amname = 'btree'
      AND pg_get_indexdef(index_row.indexrelid, 1, TRUE) = 'action'
      AND pg_get_indexdef(index_row.indexrelid, 2, TRUE) = 'request_id'
      AND pg_get_expr(index_row.indpred, index_row.indrelid, TRUE) =
          'action::text = ''system.openai.reset_credit.auto''::text AND request_id::text <> ''''::text';

    IF NOT COALESCE(index_is_exact, FALSE) THEN
        RAISE EXCEPTION
            'unsafe existing idx_audit_logs_openai_auto_reset_request_id index shape';
    END IF;
END
$$;

-- Direct worker grants form part of a Service Principal authorization
-- snapshot. Advance authz_version for every grant mutation so a remove/add ABA
-- cycle cannot make a previously resolved actor current again.
CREATE OR REPLACE FUNCTION bump_worker_permission_principal_authz_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.service_principal_id IS DISTINCT FROM NEW.service_principal_id THEN
        UPDATE service_principals
        SET authz_version = authz_version + 1,
            updated_at = statement_timestamp()
        WHERE id IN (OLD.service_principal_id, NEW.service_principal_id);
    ELSE
        UPDATE service_principals
        SET authz_version = authz_version + 1,
            updated_at = statement_timestamp()
        WHERE id = CASE
            WHEN TG_OP = 'DELETE' THEN OLD.service_principal_id
            ELSE NEW.service_principal_id
        END;
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_worker_permission_principal_authz_version
    ON service_principal_worker_permissions;
CREATE TRIGGER trg_worker_permission_principal_authz_version
AFTER INSERT OR DELETE OR UPDATE OF service_principal_id, permission_id
ON service_principal_worker_permissions
FOR EACH ROW
EXECUTE FUNCTION bump_worker_permission_principal_authz_version();

-- A first application is allowed only when both reserved codes are absent.
-- A reapplication is allowed only for the exact roleless shape produced here.
-- Any active, disabled, partial, or over-privileged collision fails closed and
-- leaves the colliding rows untouched because migrations run transactionally.
DO $$
DECLARE
    reserved_permission_id BIGINT;
    reserved_permission_description TEXT;
    reserved_principal_id BIGINT;
    reserved_principal_name TEXT;
    reserved_principal_status TEXT;
    permission_exists BOOLEAN;
    principal_exists BOOLEAN;
    role_count BIGINT;
    direct_permission_count BIGINT;
    expected_direct_permission_count BIGINT;
BEGIN
    SELECT id, description
    INTO reserved_permission_id, reserved_permission_description
    FROM permissions
    WHERE code = 'platform.account.openai_quota_auto_reset'
    FOR UPDATE;
    permission_exists := FOUND;

    SELECT id, name, status
    INTO reserved_principal_id, reserved_principal_name, reserved_principal_status
    FROM service_principals
    WHERE code = 'openai_quota_auto_reset_worker'
    FOR UPDATE;
    principal_exists := FOUND;

    IF permission_exists IS DISTINCT FROM principal_exists THEN
        RAISE EXCEPTION
            'reserved OpenAI quota auto-reset identity collision: partial seed exists';
    END IF;

    IF NOT permission_exists THEN
        INSERT INTO permissions (code, description)
        VALUES (
            'platform.account.openai_quota_auto_reset',
            'Query and consume OpenAI quota reset credits and recover the same account'
        )
        RETURNING id INTO reserved_permission_id;

        INSERT INTO service_principals (code, name, status)
        VALUES (
            'openai_quota_auto_reset_worker',
            'OpenAI Quota Auto-Reset Worker',
            'active'
        )
        RETURNING id INTO reserved_principal_id;

        INSERT INTO service_principal_worker_permissions (
            service_principal_id,
            permission_id
        )
        VALUES (reserved_principal_id, reserved_permission_id);
    ELSE
        SELECT COUNT(*)
        INTO role_count
        FROM service_principal_roles
        WHERE service_principal_id = reserved_principal_id;

        SELECT
            COUNT(*),
            COUNT(*) FILTER (WHERE permission_id = reserved_permission_id)
        INTO direct_permission_count, expected_direct_permission_count
        FROM service_principal_worker_permissions
        WHERE service_principal_id = reserved_principal_id;

        IF reserved_permission_description IS DISTINCT FROM
               'Query and consume OpenAI quota reset credits and recover the same account'
           OR reserved_principal_name IS DISTINCT FROM
               'OpenAI Quota Auto-Reset Worker'
           OR reserved_principal_status IS DISTINCT FROM 'active'
           OR role_count <> 0
           OR direct_permission_count <> 1
           OR expected_direct_permission_count <> 1 THEN
            RAISE EXCEPTION
                'reserved OpenAI quota auto-reset identity collision: unsafe existing shape';
        END IF;
    END IF;

    SELECT COUNT(*)
    INTO role_count
    FROM service_principal_roles
    WHERE service_principal_id = reserved_principal_id;

    SELECT
        COUNT(*),
        COUNT(*) FILTER (WHERE permission_id = reserved_permission_id)
    INTO direct_permission_count, expected_direct_permission_count
    FROM service_principal_worker_permissions
    WHERE service_principal_id = reserved_principal_id;

    IF role_count <> 0
       OR direct_permission_count <> 1
       OR expected_direct_permission_count <> 1 THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset worker must have zero roles and exactly one direct permission';
    END IF;
END
$$;

-- Attempts that were already executable when this migration took its snapshot
-- cannot be finalized safely by either generation of worker. An old worker
-- finalizes by record id without an audit transaction, while the new atomic
-- finalizer cannot prove whether the old upstream call completed. Keep an exact,
-- durable marker keyed to the idempotency record so status-only old-worker
-- updates and cleanup cannot silently erase the reconciliation obligation.
CREATE TABLE IF NOT EXISTS openai_quota_auto_reset_protected_attempts (
    idempotency_record_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    protected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reconciled_at TIMESTAMPTZ,
    reconciliation_audit_request_id VARCHAR(64),
    CONSTRAINT openai_auto_reset_protected_attempts_pkey
        PRIMARY KEY (idempotency_record_id),
    CONSTRAINT openai_auto_reset_protected_attempts_record_id_fkey
        FOREIGN KEY (idempotency_record_id)
        REFERENCES idempotency_records(id) ON DELETE RESTRICT,
    CONSTRAINT openai_auto_reset_protected_attempts_account_id_check
        CHECK (account_id > 0),
    CONSTRAINT openai_auto_reset_protected_attempts_reconciled_check
        CHECK (
            (reconciled_at IS NULL) =
            (reconciliation_audit_request_id IS NULL)
        )
);

-- The migration SQL is intentionally reapplicable for shape verification. This
-- singleton records that the mixed-version snapshot has already been taken, so
-- a raw reapplication cannot protect a current-version Service Principal retry.
CREATE TABLE IF NOT EXISTS openai_quota_auto_reset_protection_backfill (
    completed BOOLEAN NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT openai_auto_reset_protection_backfill_pkey
        PRIMARY KEY (completed),
    CONSTRAINT openai_auto_reset_protection_backfill_singleton
        CHECK (completed)
);

-- Neither IF NOT EXISTS table may accept a look-alike. The protection trigger
-- and one-time gate rely on these exact keys and constraints.
DO $$
DECLARE
    protected_relation_id OID := to_regclass('openai_quota_auto_reset_protected_attempts');
    backfill_relation_id OID := to_regclass('openai_quota_auto_reset_protection_backfill');
    protected_columns_are_exact BOOLEAN;
    protected_constraints_are_exact BOOLEAN;
    protected_primary_key_is_exact BOOLEAN;
    protected_record_fk_is_exact BOOLEAN;
    protected_account_check_is_exact BOOLEAN;
    protected_reconciled_check_is_exact BOOLEAN;
    backfill_columns_are_exact BOOLEAN;
    backfill_constraints_are_exact BOOLEAN;
    backfill_primary_key_is_exact BOOLEAN;
    backfill_singleton_is_exact BOOLEAN;
BEGIN
    IF protected_relation_id IS NULL OR backfill_relation_id IS NULL THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset protection tables were not created';
    END IF;

    SELECT
        COUNT(*) = 5
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'idempotency_record_id'
              AND attribute.atttypid = 'bigint'::regtype
              AND attribute.attnotnull
              AND default_value.oid IS NULL
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'account_id'
              AND attribute.atttypid = 'bigint'::regtype
              AND attribute.attnotnull
              AND default_value.oid IS NULL
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'protected_at'
              AND attribute.atttypid = 'timestamptz'::regtype
              AND attribute.attnotnull
              AND pg_get_expr(default_value.adbin, default_value.adrelid) = 'now()'
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'reconciled_at'
              AND attribute.atttypid = 'timestamptz'::regtype
              AND NOT attribute.attnotnull
              AND default_value.oid IS NULL
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'reconciliation_audit_request_id'
              AND attribute.atttypid = 'character varying'::regtype
              AND attribute.atttypmod = 68
              AND NOT attribute.attnotnull
              AND default_value.oid IS NULL
        ) = 1
    INTO protected_columns_are_exact
    FROM pg_attribute AS attribute
    LEFT JOIN pg_attrdef AS default_value
        ON default_value.adrelid = attribute.attrelid
       AND default_value.adnum = attribute.attnum
    WHERE attribute.attrelid = protected_relation_id
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped;

    SELECT
        COUNT(*) FILTER (
            WHERE constraint_row.contype IN ('p', 'f', 'u', 'c', 'x')
        ) = 4
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'p') = 1
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'f') = 1
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'c') = 2
    INTO protected_constraints_are_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = protected_relation_id;

    SELECT COUNT(*) = 1
    INTO protected_primary_key_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = protected_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protected_attempts_pkey'
      AND constraint_row.contype = 'p'
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = protected_relation_id
                AND attribute.attname = 'idempotency_record_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    SELECT COUNT(*) = 1
    INTO protected_account_check_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = protected_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protected_attempts_account_id_check'
      AND constraint_row.contype = 'c'
      AND constraint_row.convalidated
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = protected_relation_id
                AND attribute.attname = 'account_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND pg_get_expr(constraint_row.conbin, constraint_row.conrelid) = '(account_id > 0)';

    SELECT COUNT(*) = 1
    INTO protected_record_fk_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = protected_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protected_attempts_record_id_fkey'
      AND constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'idempotency_records'::regclass
      AND constraint_row.confdeltype = 'r'
      AND constraint_row.convalidated
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = protected_relation_id
                AND attribute.attname = 'idempotency_record_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND constraint_row.confkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = 'idempotency_records'::regclass
                AND attribute.attname = 'id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    SELECT COUNT(*) = 1
    INTO protected_reconciled_check_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = protected_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protected_attempts_reconciled_check'
      AND constraint_row.contype = 'c'
      AND constraint_row.convalidated
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = protected_relation_id
                AND attribute.attname = 'reconciled_at'
                AND NOT attribute.attisdropped
          ),
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = protected_relation_id
                AND attribute.attname = 'reconciliation_audit_request_id'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND pg_get_expr(constraint_row.conbin, constraint_row.conrelid) =
          '((reconciled_at IS NULL) = (reconciliation_audit_request_id IS NULL))';

    SELECT
        COUNT(*) = 2
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'completed'
              AND attribute.atttypid = 'boolean'::regtype
              AND attribute.attnotnull
              AND default_value.oid IS NULL
        ) = 1
        AND COUNT(*) FILTER (
            WHERE attribute.attname = 'completed_at'
              AND attribute.atttypid = 'timestamptz'::regtype
              AND attribute.attnotnull
              AND pg_get_expr(default_value.adbin, default_value.adrelid) = 'now()'
        ) = 1
    INTO backfill_columns_are_exact
    FROM pg_attribute AS attribute
    LEFT JOIN pg_attrdef AS default_value
        ON default_value.adrelid = attribute.attrelid
       AND default_value.adnum = attribute.attnum
    WHERE attribute.attrelid = backfill_relation_id
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped;

    SELECT
        COUNT(*) FILTER (
            WHERE constraint_row.contype IN ('p', 'f', 'u', 'c', 'x')
        ) = 2
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'p') = 1
        AND COUNT(*) FILTER (WHERE constraint_row.contype = 'c') = 1
    INTO backfill_constraints_are_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = backfill_relation_id;

    SELECT COUNT(*) = 1
    INTO backfill_primary_key_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = backfill_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protection_backfill_pkey'
      AND constraint_row.contype = 'p'
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = backfill_relation_id
                AND attribute.attname = 'completed'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[];

    SELECT COUNT(*) = 1
    INTO backfill_singleton_is_exact
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = backfill_relation_id
      AND constraint_row.conname = 'openai_auto_reset_protection_backfill_singleton'
      AND constraint_row.contype = 'c'
      AND constraint_row.convalidated
      AND constraint_row.conkey = ARRAY[
          (
              SELECT attribute.attnum
              FROM pg_attribute AS attribute
              WHERE attribute.attrelid = backfill_relation_id
                AND attribute.attname = 'completed'
                AND NOT attribute.attisdropped
          )
      ]::SMALLINT[]
      AND pg_get_expr(constraint_row.conbin, constraint_row.conrelid) = 'completed';

    IF NOT COALESCE(protected_columns_are_exact, FALSE)
       OR NOT COALESCE(protected_constraints_are_exact, FALSE)
       OR NOT COALESCE(protected_primary_key_is_exact, FALSE)
       OR NOT COALESCE(protected_record_fk_is_exact, FALSE)
       OR NOT COALESCE(protected_account_check_is_exact, FALSE)
       OR NOT COALESCE(protected_reconciled_check_is_exact, FALSE) THEN
        RAISE EXCEPTION
            'unsafe existing openai_quota_auto_reset_protected_attempts table shape';
    END IF;

    IF NOT COALESCE(backfill_columns_are_exact, FALSE)
       OR NOT COALESCE(backfill_constraints_are_exact, FALSE)
       OR NOT COALESCE(backfill_primary_key_is_exact, FALSE)
       OR NOT COALESCE(backfill_singleton_is_exact, FALSE) THEN
        RAISE EXCEPTION
            'unsafe existing openai_quota_auto_reset_protection_backfill table shape';
    END IF;
END
$$;

-- Existing releases persisted executable state under account-qualified,
-- Service-Principal-qualified, and oldest raw scopes. Move every account row so
-- completed replay remains discoverable, plus every recognizable SP/raw row
-- selected by the one-time protection snapshot, to the reserved Service
-- Principal. The raw upgrade fence, malformed raw rows, and current attempts
-- created after the snapshot stay outside this predicate.
-- A target (scope, key hash) collision aborts the migration via the existing
-- unique index rather than choosing a record and risking a second reset.
-- Own the idempotency snapshot before selecting it. Raw/SP account provenance
-- is selected and persisted in one statement below, so concurrent account
-- metadata changes cannot separate candidate validation from marker insertion.
LOCK TABLE idempotency_records IN SHARE ROW EXCLUSIVE MODE;

-- A previous raw application may already have installed the attempt guard. The
-- table lock closes all writer windows while migration-owned normalization runs;
-- recreate the guard before releasing that lock at transaction commit.
DROP TRIGGER IF EXISTS idempotency_records_openai_auto_reset_protected_attempt_guard
    ON idempotency_records;

-- Take this snapshot exactly once. Account scope carries provenance directly,
-- including after an account or its recovery state has disappeared. When an
-- account-scoped row does still match a pending state exactly, verify the same
-- semantic fingerprint that runtime recovery accepts. Raw/SP scope must match
-- exactly one pending account state through both the stable key and either the
-- legacy account actor or reserved Service Principal fingerprint. The payload
-- text below intentionally reproduces encoding/json's compact sorted-key output
-- used by BuildIdempotencyFingerprint:
-- {"account_id":<id>,"credit_hash":"<hash>","cycle_hash":"<hash>"}.
-- Zero, multiple, malformed, or semantically mismatched candidates abort the
-- migration instead of assigning audit identity by guess. The marker has no
-- account FK so persisted provenance survives a later physical account delete.
DO $$
DECLARE
    snapshot_started BOOLEAN := FALSE;
    worker_principal_id BIGINT;
BEGIN
    INSERT INTO openai_quota_auto_reset_protection_backfill (completed)
    VALUES (TRUE)
    ON CONFLICT (completed) DO NOTHING
    RETURNING completed INTO snapshot_started;
    snapshot_started := FOUND;

    SELECT principal.id
    INTO worker_principal_id
    FROM service_principals AS principal
    WHERE principal.code = 'openai_quota_auto_reset_worker';

    IF worker_principal_id IS NULL THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protection snapshot worker identity is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    IF snapshot_started THEN

    -- Every in-flight row that this migration recognizes as an executable
    -- historical auto-reset attempt must carry canonical persisted identities.
    -- The raw upgrade fence is deliberately not executable and remains outside
    -- the protected set.
    IF EXISTS (
        SELECT 1
        FROM idempotency_records AS record
        WHERE record.status IN ('processing', 'failed_retryable')
          AND (
              LEFT(
                  record.scope,
                  LENGTH('openai_auto_reset_credit|account:')
              ) = 'openai_auto_reset_credit|account:'
              OR LEFT(
                  record.scope,
                  LENGTH('openai_auto_reset_credit|service_principal:')
              ) = 'openai_auto_reset_credit|service_principal:'
              OR (
                  record.scope = 'openai_auto_reset_credit'
                  AND record.request_fingerprint IS DISTINCT FROM
                      'upgrade-fence:actor-qualified:v1'
              )
          )
          AND (
              record.idempotency_key_hash IS NULL
              OR record.idempotency_key_hash !~ '^[0-9a-f]{64}$'
              OR record.request_fingerprint IS NULL
              OR record.request_fingerprint !~ '^[0-9a-f]{64}$'
              OR (
                  LEFT(
                      record.scope,
                      LENGTH('openai_auto_reset_credit|account:')
                  ) = 'openai_auto_reset_credit|account:'
                  AND (
                      record.scope !~
                          '^openai_auto_reset_credit\|account:[1-9][0-9]*$'
                      OR SUBSTRING(
                          record.scope FROM
                          '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
                      )::NUMERIC > 9223372036854775807
                  )
              )
              OR (
                  LEFT(
                      record.scope,
                      LENGTH('openai_auto_reset_credit|service_principal:')
                  ) = 'openai_auto_reset_credit|service_principal:'
                  AND (
                      record.scope !~
                          '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
                      OR SUBSTRING(
                          record.scope FROM
                          '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
                      )::NUMERIC > 9223372036854775807
                  )
              )
          )
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protected attempt identity is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO openai_quota_auto_reset_protected_attempts (
        idempotency_record_id,
        account_id
    )
    SELECT record.id,
           SUBSTRING(
               record.scope FROM
               '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
           )::BIGINT
    FROM idempotency_records AS record
    WHERE record.status IN ('processing', 'failed_retryable')
      AND record.scope ~ '^openai_auto_reset_credit\|account:[1-9][0-9]*$';

    INSERT INTO openai_quota_auto_reset_protected_attempts (
        idempotency_record_id,
        account_id
    )
    SELECT record.id, MIN(candidate.id)
    FROM idempotency_records AS record
    JOIN accounts AS candidate
      ON candidate.platform = 'openai'
     AND candidate.extra -> 'codex_auto_reset_credit_state' ->> 'status'
         IN ('resetting', 'failed')
     AND candidate.extra -> 'codex_auto_reset_credit_state' ->>
         'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
     AND candidate.extra -> 'codex_auto_reset_credit_state' ->>
         'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
     AND record.idempotency_key_hash = ENCODE(
         SHA256(CONVERT_TO(
             'oarc:' || candidate.id::TEXT || ':' ||
             (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                 'attempt_credit_hash') || ':' ||
             (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                 'attempt_cycle_hash'),
             'UTF8'
         )),
         'hex'
     )
    WHERE record.status IN ('processing', 'failed_retryable')
      AND (
          record.scope ~
              '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
          OR (
              record.scope = 'openai_auto_reset_credit'
              AND record.request_fingerprint IS DISTINCT FROM
                  'upgrade-fence:actor-qualified:v1'
          )
      )
    GROUP BY record.id
    HAVING COUNT(*) = 1;

    -- Read only the locked attempt set and the provenance persisted by the
    -- preceding statement. Both zero and multiple account matches leave no
    -- marker and fail closed.
    IF EXISTS (
        SELECT 1
        FROM idempotency_records AS record
        LEFT JOIN openai_quota_auto_reset_protected_attempts AS protected
          ON protected.idempotency_record_id = record.id
        WHERE record.status IN ('processing', 'failed_retryable')
          AND (
              record.scope ~
                  '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
              OR (
                  record.scope = 'openai_auto_reset_credit'
                  AND record.request_fingerprint IS DISTINCT FROM
                      'upgrade-fence:actor-qualified:v1'
              )
          )
          AND protected.idempotency_record_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protected attempt account provenance is not unique'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM idempotency_records AS record
        WHERE record.status IN ('processing', 'failed_retryable')
          AND (
              record.scope ~ '^openai_auto_reset_credit\|account:[1-9][0-9]*$'
              OR record.scope ~
                  '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
              OR (
                  record.scope = 'openai_auto_reset_credit'
                  AND record.request_fingerprint IS DISTINCT FROM
                      'upgrade-fence:actor-qualified:v1'
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM openai_quota_auto_reset_protected_attempts AS protected
              WHERE protected.idempotency_record_id = record.id
          )
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protection snapshot is incomplete'
            USING ERRCODE = 'check_violation';
    END IF;
    END IF;

    -- Reapplication must harden markers written by earlier drafts even though
    -- their one-time discovery sentinel already exists. Missing accounts and
    -- newer/different account states remain valid manual-reconciliation cases.
    IF EXISTS (
        SELECT 1
        FROM openai_quota_auto_reset_protected_attempts AS protected
        JOIN idempotency_records AS record
          ON record.id = protected.idempotency_record_id
        WHERE record.idempotency_key_hash IS NULL
           OR record.idempotency_key_hash !~ '^[0-9a-f]{64}$'
           OR record.request_fingerprint IS NULL
           OR record.request_fingerprint !~ '^[0-9a-f]{64}$'
           OR NOT (
               record.scope = 'openai_auto_reset_credit'
               OR record.scope =
                  'openai_auto_reset_credit|account:' ||
                  protected.account_id::TEXT
               OR (
                   record.scope ~
                       '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
                   AND SUBSTRING(
                       record.scope FROM
                       '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
                   )::NUMERIC <= 9223372036854775807
               )
           )
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protected marker identity is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM openai_quota_auto_reset_protected_attempts AS protected
        JOIN idempotency_records AS record
          ON record.id = protected.idempotency_record_id
        WHERE protected.reconciled_at IS NULL
          AND NOT (
              (
                  record.status = 'processing'
                  AND record.response_status IS NULL
                  AND record.response_body IS NULL
                  AND record.error_reason IS NULL
              )
              OR (
                  record.status = 'failed_retryable'
                  AND record.response_status IS NULL
                  AND record.response_body IS NULL
              )
          )
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset unresolved protected parent shape mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM openai_quota_auto_reset_protected_attempts AS protected
        JOIN idempotency_records AS record
          ON record.id = protected.idempotency_record_id
        JOIN accounts AS candidate
          ON candidate.id = protected.account_id
         AND candidate.extra -> 'codex_auto_reset_credit_state' ->> 'status'
             IN ('resetting', 'failed')
         AND candidate.extra -> 'codex_auto_reset_credit_state' ->>
             'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
         AND candidate.extra -> 'codex_auto_reset_credit_state' ->>
             'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
         AND record.idempotency_key_hash = ENCODE(
             SHA256(CONVERT_TO(
                 'oarc:' || candidate.id::TEXT || ':' ||
                 (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                     'attempt_credit_hash') || ':' ||
                 (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                     'attempt_cycle_hash'),
                 'UTF8'
             )),
             'hex'
         )
        CROSS JOIN LATERAL (
            SELECT
                '{"account_id":' || candidate.id::TEXT ||
                ',"credit_hash":"' ||
                (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                    'attempt_credit_hash') ||
                '","cycle_hash":"' ||
                (candidate.extra -> 'codex_auto_reset_credit_state' ->>
                    'attempt_cycle_hash') || '"}' AS canonical_payload
        ) AS payload
        WHERE record.request_fingerprint IS DISTINCT FROM ENCODE(
              SHA256(CONVERT_TO(
                  'POST' || CHR(10) ||
                  '/system/openai/reset-credit/auto' || CHR(10) ||
                  'account:' || candidate.id::TEXT || CHR(10) ||
                  payload.canonical_payload,
                  'UTF8'
              )),
              'hex'
          )
          AND record.request_fingerprint IS DISTINCT FROM ENCODE(
              SHA256(CONVERT_TO(
                  'POST' || CHR(10) ||
                  '/system/openai/reset-credit/auto' || CHR(10) ||
                  'service_principal:' || worker_principal_id::TEXT || CHR(10) ||
                  payload.canonical_payload,
                  'UTF8'
              )),
              'hex'
          )
    ) THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset protected marker fingerprint mismatch'
            USING ERRCODE = 'check_violation';
    END IF;
END
$$;

-- Earlier drafts exposed caller-controlled response bodies and retention. A raw
-- reapplication must not merely remove those overloads while accepting outcomes
-- they already wrote. Every retained reconciliation tombstone must satisfy the
-- complete hardened parent and audit contract or block for operator review.
DO $$
DECLARE
    worker_principal_id BIGINT;
    outcome RECORD;
    outcome_audit audit_logs%ROWTYPE;
    result_code TEXT;
    windows_reset NUMERIC;
    expected_response_body TEXT;
BEGIN
    SELECT principal.id
    INTO worker_principal_id
    FROM service_principals AS principal
    WHERE principal.code = 'openai_quota_auto_reset_worker';

    IF worker_principal_id IS NULL THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset reconciled outcome worker identity is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    FOR outcome IN
        SELECT protected.account_id,
               protected.reconciled_at,
               protected.reconciliation_audit_request_id,
               record.*
        FROM openai_quota_auto_reset_protected_attempts AS protected
        JOIN idempotency_records AS record
          ON record.id = protected.idempotency_record_id
        WHERE protected.reconciled_at IS NOT NULL
    LOOP
        IF outcome.reconciliation_audit_request_id IS DISTINCT FROM
           ('reconcile-success:' || outcome.id::TEXT) THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset reconciled outcome audit identity mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT audit.*
        INTO outcome_audit
        FROM audit_logs AS audit
        WHERE audit.action = 'system.openai.reset_credit.auto'
          AND audit.request_id = outcome.reconciliation_audit_request_id;

        IF NOT FOUND
           OR outcome_audit.actor_user_id IS NOT NULL
           OR outcome_audit.actor_service_principal_id IS DISTINCT FROM
              worker_principal_id
           OR outcome_audit.actor_email IS DISTINCT FROM ''
           OR outcome_audit.actor_role IS DISTINCT FROM ''
           OR outcome_audit.auth_method IS DISTINCT FROM 'service_principal'
           OR outcome_audit.credential_masked IS DISTINCT FROM ''
           OR outcome_audit.action IS DISTINCT FROM
              'system.openai.reset_credit.auto'
           OR outcome_audit.method IS DISTINCT FROM 'SYSTEM'
           OR outcome_audit.path IS DISTINCT FROM
              ('/system/openai/accounts/' || outcome.account_id::TEXT ||
               '/auto-reset-credit')
           OR outcome_audit.request_id IS DISTINCT FROM
              outcome.reconciliation_audit_request_id
           OR outcome_audit.client_ip IS DISTINCT FROM ''
           OR outcome_audit.user_agent IS DISTINCT FROM ''
           OR outcome_audit.request_body IS DISTINCT FROM ''
           OR outcome_audit.latency_ms IS DISTINCT FROM 0
           OR outcome_audit.extra IS NULL
           OR JSONB_TYPEOF(outcome_audit.extra) IS DISTINCT FROM 'object'
           OR outcome_audit.extra -> 'account_id' IS DISTINCT FROM
              TO_JSONB(outcome.account_id)
           OR outcome_audit.extra -> 'idempotency_record_id' IS DISTINCT FROM
              TO_JSONB(outcome.id)
           OR JSONB_TYPEOF(outcome_audit.extra -> 'request_fingerprint')
              IS DISTINCT FROM 'string'
           OR outcome_audit.extra ->> 'request_fingerprint' IS DISTINCT FROM
              outcome.request_fingerprint
           OR JSONB_TYPEOF(outcome_audit.extra -> 'evidence_ref')
              IS DISTINCT FROM 'string'
           OR LENGTH(BTRIM(outcome_audit.extra ->> 'evidence_ref'))
              NOT BETWEEN 1 AND 256
           OR JSONB_TYPEOF(outcome_audit.extra -> 'decision_owner')
              IS DISTINCT FROM 'string'
           OR LENGTH(BTRIM(outcome_audit.extra ->> 'decision_owner'))
              NOT BETWEEN 1 AND 128
           OR JSONB_TYPEOF(outcome_audit.extra -> 'result_code')
              IS DISTINCT FROM 'string'
           OR (
               (outcome_audit.extra ->> 'result_code') = ANY (ARRAY[
                   'reconciled_success',
                   'no_credit',
                   'recovery_deferred',
                   'recovery_failed'
               ])
              ) IS DISTINCT FROM TRUE
           OR JSONB_TYPEOF(outcome_audit.extra -> 'windows_reset')
              IS DISTINCT FROM 'number' THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset reconciled outcome audit contract mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        result_code := outcome_audit.extra ->> 'result_code';
        windows_reset := (outcome_audit.extra ->> 'windows_reset')::NUMERIC;
        IF windows_reset < 0
           OR windows_reset <> TRUNC(windows_reset)
           OR windows_reset > 2147483647
           OR (result_code = 'no_credit' AND windows_reset <> 0)
           OR outcome_audit.status_code IS DISTINCT FROM (CASE
               WHEN result_code = 'reconciled_success' THEN 200
               ELSE 409
              END) THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset reconciled outcome result contract mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        expected_response_body := CASE result_code
            WHEN 'no_credit' THEN JSONB_BUILD_OBJECT(
                'result_code', 'no_credit',
                'windows_reset', 0
            )::TEXT
            WHEN 'recovery_deferred' THEN JSONB_BUILD_OBJECT(
                'account_state_recovered', FALSE,
                'recovery_deferred', TRUE,
                'recovery_pending', TRUE,
                'result_code', 'success',
                'windows_reset', windows_reset::BIGINT
            )::TEXT
            WHEN 'recovery_failed' THEN JSONB_BUILD_OBJECT(
                'account_state_recovered', FALSE,
                'recovery_pending', TRUE,
                'result_code', 'success',
                'warning_code',
                    'OPENAI_AUTO_RESET_RECONCILED_RECOVERY_FAILED',
                'windows_reset', windows_reset::BIGINT
            )::TEXT
            ELSE JSONB_BUILD_OBJECT(
                'account_state_recovered', FALSE,
                'recovery_pending', TRUE,
                'result_code', 'success',
                'windows_reset', windows_reset::BIGINT
            )::TEXT
        END;

        IF outcome.scope IS DISTINCT FROM
           ('openai_auto_reset_credit|service_principal:' ||
            worker_principal_id::TEXT)
           OR outcome.status IS DISTINCT FROM 'succeeded'
           OR outcome.response_status IS DISTINCT FROM 200
           OR outcome.response_body IS DISTINCT FROM expected_response_body
           OR outcome.error_reason IS NOT NULL
           OR outcome.locked_until IS NOT NULL
           OR outcome.expires_at IS DISTINCT FROM
              outcome.reconciled_at + INTERVAL '8 days' THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset reconciled parent outcome mismatch'
                USING ERRCODE = 'check_violation';
        END IF;
    END LOOP;
END
$$;

-- A successful no-effect decision has no parent or marker left to inspect, so
-- its deterministic audit is the durable tombstone. Validate every audit that
-- claims either side of that namespace, including drafts with only one side,
-- and reject an exact still-pending account state that hardened discard would
-- have removed.
DO $$
DECLARE
    worker_principal_id BIGINT;
    decision_audit audit_logs%ROWTYPE;
    request_suffix TEXT;
    record_number NUMERIC;
    account_number NUMERIC;
    record_id BIGINT;
    account_id BIGINT;
    request_fingerprint TEXT;
BEGIN
    SELECT principal.id
    INTO worker_principal_id
    FROM service_principals AS principal
    WHERE principal.code = 'openai_quota_auto_reset_worker';

    IF worker_principal_id IS NULL THEN
        RAISE EXCEPTION
            'OpenAI quota auto-reset no-effect audit worker identity is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    FOR decision_audit IN
        SELECT audit.*
        FROM audit_logs AS audit
        WHERE audit.action = 'system.openai.reset_credit.auto'
          AND (
              LEFT(audit.request_id, LENGTH('reconcile-no-effect:')) =
                  'reconcile-no-effect:'
              OR audit.extra ->> 'result_code' = 'reconciled_no_effect'
          )
    LOOP
        request_suffix := SUBSTRING(
            decision_audit.request_id FROM
            '^reconcile-no-effect:([1-9][0-9]*)$'
        );

        IF request_suffix IS NULL
           OR request_suffix::NUMERIC > 9223372036854775807
           OR decision_audit.actor_user_id IS NOT NULL
           OR decision_audit.actor_service_principal_id IS DISTINCT FROM
              worker_principal_id
           OR decision_audit.actor_email IS DISTINCT FROM ''
           OR decision_audit.actor_role IS DISTINCT FROM ''
           OR decision_audit.auth_method IS DISTINCT FROM 'service_principal'
           OR decision_audit.credential_masked IS DISTINCT FROM ''
           OR decision_audit.method IS DISTINCT FROM 'SYSTEM'
           OR decision_audit.client_ip IS DISTINCT FROM ''
           OR decision_audit.user_agent IS DISTINCT FROM ''
           OR decision_audit.request_body IS DISTINCT FROM ''
           OR decision_audit.status_code IS DISTINCT FROM 409
           OR decision_audit.latency_ms IS DISTINCT FROM 0
           OR decision_audit.extra IS NULL
           OR JSONB_TYPEOF(decision_audit.extra) IS DISTINCT FROM 'object'
           OR decision_audit.extra ->> 'result_code' IS DISTINCT FROM
              'reconciled_no_effect'
           OR decision_audit.extra -> 'windows_reset' IS DISTINCT FROM
              TO_JSONB(0)
           OR JSONB_TYPEOF(decision_audit.extra -> 'account_id')
              IS DISTINCT FROM 'number'
           OR JSONB_TYPEOF(decision_audit.extra -> 'idempotency_record_id')
              IS DISTINCT FROM 'number'
           OR JSONB_TYPEOF(decision_audit.extra -> 'request_fingerprint')
              IS DISTINCT FROM 'string'
           OR decision_audit.extra ->> 'request_fingerprint'
              !~ '^[0-9a-f]{64}$'
           OR JSONB_TYPEOF(decision_audit.extra -> 'evidence_ref')
              IS DISTINCT FROM 'string'
           OR LENGTH(BTRIM(decision_audit.extra ->> 'evidence_ref'))
              NOT BETWEEN 1 AND 256
           OR JSONB_TYPEOF(decision_audit.extra -> 'decision_owner')
              IS DISTINCT FROM 'string'
           OR LENGTH(BTRIM(decision_audit.extra ->> 'decision_owner'))
              NOT BETWEEN 1 AND 128 THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset no-effect audit contract mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        record_number :=
            (decision_audit.extra ->> 'idempotency_record_id')::NUMERIC;
        account_number := (decision_audit.extra ->> 'account_id')::NUMERIC;
        IF record_number <= 0
           OR record_number <> TRUNC(record_number)
           OR record_number > 9223372036854775807
           OR account_number <= 0
           OR account_number <> TRUNC(account_number)
           OR account_number > 9223372036854775807
           OR request_suffix::NUMERIC <> record_number THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset no-effect audit identity mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        record_id := record_number::BIGINT;
        account_id := account_number::BIGINT;
        request_fingerprint :=
            decision_audit.extra ->> 'request_fingerprint';

        IF decision_audit.request_id IS DISTINCT FROM
           ('reconcile-no-effect:' || record_id::TEXT)
           OR decision_audit.path IS DISTINCT FROM
              ('/system/openai/accounts/' || account_id::TEXT ||
               '/auto-reset-credit')
           OR decision_audit.extra -> 'account_id' IS DISTINCT FROM
              TO_JSONB(account_id)
           OR decision_audit.extra -> 'idempotency_record_id'
              IS DISTINCT FROM TO_JSONB(record_id)
           OR EXISTS (
               SELECT 1
               FROM idempotency_records AS record
               WHERE record.id = record_id
           )
           OR EXISTS (
               SELECT 1
               FROM openai_quota_auto_reset_protected_attempts AS protected
               WHERE protected.idempotency_record_id = record_id
           ) THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset no-effect audit cleanup mismatch'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM accounts AS account
            CROSS JOIN LATERAL (
                SELECT
                    '{"account_id":' || account.id::TEXT ||
                    ',"credit_hash":"' ||
                    (account.extra -> 'codex_auto_reset_credit_state' ->>
                        'attempt_credit_hash') ||
                    '","cycle_hash":"' ||
                    (account.extra -> 'codex_auto_reset_credit_state' ->>
                        'attempt_cycle_hash') || '"}' AS canonical_payload
            ) AS payload
            WHERE account.id = account_id
              AND account.extra -> 'codex_auto_reset_credit_state' ->> 'status'
                  IN ('resetting', 'failed')
              AND account.extra -> 'codex_auto_reset_credit_state' ->>
                  'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
              AND account.extra -> 'codex_auto_reset_credit_state' ->>
                  'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
              AND (
                  request_fingerprint = ENCODE(
                      SHA256(CONVERT_TO(
                          'POST' || CHR(10) ||
                          '/system/openai/reset-credit/auto' || CHR(10) ||
                          'account:' || account.id::TEXT || CHR(10) ||
                          payload.canonical_payload,
                          'UTF8'
                      )),
                      'hex'
                  )
                  OR request_fingerprint = ENCODE(
                      SHA256(CONVERT_TO(
                          'POST' || CHR(10) ||
                          '/system/openai/reset-credit/auto' || CHR(10) ||
                          'service_principal:' || worker_principal_id::TEXT ||
                          CHR(10) || payload.canonical_payload,
                          'UTF8'
                      )),
                      'hex'
                  )
              )
        ) THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset no-effect audit retained pending account state'
                USING ERRCODE = 'check_violation';
        END IF;
    END LOOP;
END
$$;

-- An old worker could have classified an ambiguous upstream timeout as
-- retryable before the snapshot. Freeze every marked retryable as processing;
-- only explicit reconciliation may select its audit and terminal state.
UPDATE idempotency_records AS record
SET status = 'processing',
    error_reason = NULL,
    locked_until = NULL,
    updated_at = statement_timestamp()
FROM openai_quota_auto_reset_protected_attempts AS protected
WHERE protected.idempotency_record_id = record.id
  AND record.status = 'failed_retryable';

UPDATE idempotency_records AS record
SET scope = 'openai_auto_reset_credit|service_principal:' || principal.id::TEXT
FROM service_principals AS principal
WHERE principal.code = 'openai_quota_auto_reset_worker'
  AND (
      record.scope ~ '^openai_auto_reset_credit\|account:[1-9][0-9]*$'
      OR (
          EXISTS (
              SELECT 1
              FROM openai_quota_auto_reset_protected_attempts AS protected
              WHERE protected.idempotency_record_id = record.id
          )
          AND (
              record.scope ~ '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
              OR (
                  record.scope = 'openai_auto_reset_credit'
                  AND record.request_fingerprint ~ '^[0-9a-f]{64}$'
              )
          )
      )
  );

-- A protected snapshot row is immutable even when an old worker still holds its
-- id and issues a status-only MarkSucceeded or MarkFailedRetryable after the
-- migration commits. Reconciliation does not remove that protection: it records
-- the exact audit identity on a permanent tombstone before making the sole
-- processing -> succeeded transition. A waiting old UPDATE therefore sees the
-- tombstone after reconciliation commits and is rejected when it rechecks the
-- row. Unresolved DELETE returns no row. Terminal cleanup deletes a reconciled
-- tombstone immediately before its parent row; the RESTRICT foreign key remains
-- a fail-closed backstop if this trigger is disabled.
CREATE OR REPLACE FUNCTION guard_openai_quota_auto_reset_protected_attempt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    protected_account_id BIGINT;
    protected_reconciled_at TIMESTAMPTZ;
    protected_audit_request_id VARCHAR(64);
BEGIN
    SELECT protected.account_id,
           protected.reconciled_at,
           protected.reconciliation_audit_request_id
    INTO protected_account_id,
         protected_reconciled_at,
         protected_audit_request_id
    FROM openai_quota_auto_reset_protected_attempts AS protected
    WHERE protected.idempotency_record_id = OLD.id;

    IF FOUND THEN
        IF TG_OP = 'DELETE' THEN
            IF protected_reconciled_at IS NULL THEN
                RETURN NULL;
            END IF;

            IF EXISTS (
                SELECT 1
                FROM accounts AS account
                WHERE account.id = protected_account_id
                  AND account.extra -> 'codex_auto_reset_credit_state' ->>
                      'status' IN ('resetting', 'failed')
                  AND account.extra -> 'codex_auto_reset_credit_state' ->>
                      'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
                  AND account.extra -> 'codex_auto_reset_credit_state' ->>
                      'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
                  AND OLD.idempotency_key_hash = ENCODE(
                      SHA256(CONVERT_TO(
                          'oarc:' || protected_account_id::TEXT || ':' ||
                          (account.extra -> 'codex_auto_reset_credit_state' ->>
                              'attempt_credit_hash') || ':' ||
                          (account.extra -> 'codex_auto_reset_credit_state' ->>
                              'attempt_cycle_hash'),
                          'UTF8'
                      )),
                      'hex'
                  )
            ) THEN
                RETURN NULL;
            END IF;

            DELETE FROM openai_quota_auto_reset_protected_attempts
            WHERE idempotency_record_id = OLD.id
              AND reconciled_at IS NOT NULL;
            RETURN OLD;
        END IF;

        -- The marker transition and audit are invisible outside the explicit
        -- reconciliation transaction. Once that transaction commits, OLD.status
        -- is already succeeded, so a blocked generic UPDATE cannot satisfy this
        -- one-time transition even if its requested values happen to match.
        IF protected_reconciled_at IS NOT NULL
           AND OLD.status = 'processing'
           AND NEW.status = 'succeeded'
           AND NEW.id IS NOT DISTINCT FROM OLD.id
           AND NEW.scope IS NOT DISTINCT FROM OLD.scope
           AND NEW.idempotency_key_hash IS NOT DISTINCT FROM OLD.idempotency_key_hash
           AND NEW.request_fingerprint IS NOT DISTINCT FROM OLD.request_fingerprint
           AND NEW.created_at IS NOT DISTINCT FROM OLD.created_at
           AND NEW.response_status = 200
           AND NULLIF(NEW.response_body, '') IS NOT NULL
           AND NEW.error_reason IS NULL
           AND NEW.locked_until IS NULL
           AND NEW.expires_at = protected_reconciled_at + INTERVAL '8 days'
           AND EXISTS (
               SELECT 1
               FROM audit_logs AS audit
               WHERE audit.action = 'system.openai.reset_credit.auto'
                 AND audit.request_id = protected_audit_request_id
                 AND audit.actor_user_id IS NULL
                 AND OLD.scope =
                     'openai_auto_reset_credit|service_principal:' ||
                     audit.actor_service_principal_id::TEXT
                 AND audit.auth_method = 'service_principal'
                 AND audit.method = 'SYSTEM'
                 AND audit.path =
                     '/system/openai/accounts/' || protected_account_id::TEXT ||
                     '/auto-reset-credit'
                 AND audit.extra -> 'account_id' =
                     TO_JSONB(protected_account_id)
           ) THEN
            RETURN NEW;
        END IF;

        RAISE EXCEPTION
            'protected OpenAI quota auto-reset attempt requires explicit reconciliation: %', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER idempotency_records_openai_auto_reset_protected_attempt_guard
BEFORE UPDATE OR DELETE
ON idempotency_records
FOR EACH ROW
EXECUTE FUNCTION guard_openai_quota_auto_reset_protected_attempt();

-- This is the only supported success reconciliation entry point. It owns both
-- rows, writes or verifies the exact Service Principal audit, transitions the
-- marker into a permanent tombstone, and writes the terminal result in the same
-- transaction. PostgreSQL functions execute inside the caller's transaction;
-- every mutation below rolls back together on error.
-- Remove the earlier draft overload that accepted a caller-controlled expiry;
-- CREATE OR REPLACE on the hardened signature would otherwise leave it callable.
DROP FUNCTION IF EXISTS reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    INTEGER,
    TEXT,
    TIMESTAMPTZ,
    TIMESTAMPTZ,
    TEXT,
    INTEGER,
    JSONB
);

DROP FUNCTION IF EXISTS reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    INTEGER,
    TEXT,
    TIMESTAMPTZ,
    TEXT,
    INTEGER,
    JSONB
);

CREATE OR REPLACE FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(
    p_idempotency_record_id BIGINT,
    p_actor_qualified_scope TEXT,
    p_request_fingerprint TEXT,
    p_account_id BIGINT,
    p_audit_created_at TIMESTAMPTZ,
    p_audit_request_id TEXT,
    p_audit_status_code INTEGER,
    p_audit_extra JSONB
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY INVOKER
AS $$
DECLARE
    stored_record idempotency_records%ROWTYPE;
    stored_account_id BIGINT;
    stored_protected_at TIMESTAMPTZ;
    stored_reconciled_at TIMESTAMPTZ;
    stored_audit_request_id VARCHAR(64);
    worker_principal_id BIGINT;
    expected_scope TEXT;
    expected_path TEXT;
    result_code TEXT;
    windows_reset BIGINT;
    canonical_response_body TEXT;
    stored_audit_id BIGINT;
    audit_matches BOOLEAN;
    affected_rows INTEGER;
BEGIN
    LOCK TABLE idempotency_records IN ROW EXCLUSIVE MODE;

    IF p_idempotency_record_id IS NULL
       OR p_idempotency_record_id <= 0
       OR p_actor_qualified_scope IS NULL
       OR p_request_fingerprint IS NULL
       OR p_account_id IS NULL
       OR p_account_id <= 0
       OR p_audit_created_at IS NULL
       OR p_audit_status_code IS NULL
       OR p_audit_status_code <> ALL (ARRAY[200, 409])
       OR p_request_fingerprint !~ '^[0-9a-f]{64}$'
       OR p_audit_request_id IS NULL
       OR LENGTH(p_audit_request_id) NOT BETWEEN 1 AND 64
       OR p_audit_request_id !~ '^[!-~]+$'
       OR p_audit_request_id IS DISTINCT FROM
          ('reconcile-success:' || p_idempotency_record_id::TEXT)
       OR p_audit_extra IS NULL
       OR JSONB_TYPEOF(p_audit_extra) <> 'object'
       OR p_audit_extra -> 'account_id' IS DISTINCT FROM TO_JSONB(p_account_id)
       OR p_audit_extra -> 'idempotency_record_id' IS DISTINCT FROM
          TO_JSONB(p_idempotency_record_id)
       OR JSONB_TYPEOF(p_audit_extra -> 'request_fingerprint')
          IS DISTINCT FROM 'string'
       OR p_audit_extra ->> 'request_fingerprint' IS DISTINCT FROM
          p_request_fingerprint
       OR JSONB_TYPEOF(p_audit_extra -> 'evidence_ref') IS DISTINCT FROM 'string'
       OR LENGTH(BTRIM(p_audit_extra ->> 'evidence_ref')) NOT BETWEEN 1 AND 256
       OR JSONB_TYPEOF(p_audit_extra -> 'decision_owner') IS DISTINCT FROM 'string'
       OR LENGTH(BTRIM(p_audit_extra ->> 'decision_owner')) NOT BETWEEN 1 AND 128
       OR JSONB_TYPEOF(p_audit_extra -> 'result_code') IS DISTINCT FROM 'string'
       OR (
           (p_audit_extra ->> 'result_code') = ANY (ARRAY[
               'reconciled_success',
               'no_credit',
               'recovery_deferred',
               'recovery_failed'
           ])
       ) IS DISTINCT FROM TRUE
       OR (CASE
           WHEN JSONB_TYPEOF(p_audit_extra -> 'windows_reset') = 'number'
               THEN (p_audit_extra ->> 'windows_reset')::NUMERIC < 0
                    OR (p_audit_extra ->> 'windows_reset')::NUMERIC <>
                       TRUNC((p_audit_extra ->> 'windows_reset')::NUMERIC)
                    OR (p_audit_extra ->> 'windows_reset')::NUMERIC > 2147483647
           ELSE TRUE
          END)
       OR (
           p_audit_extra ->> 'result_code' = 'no_credit'
           AND p_audit_extra -> 'windows_reset' IS DISTINCT FROM TO_JSONB(0)
       )
       OR p_audit_status_code IS DISTINCT FROM (CASE
           WHEN p_audit_extra ->> 'result_code' = 'reconciled_success' THEN 200
           ELSE 409
          END) THEN
        RAISE EXCEPTION 'invalid OpenAI quota auto-reset reconciliation input'
            USING ERRCODE = 'check_violation';
    END IF;

    result_code := p_audit_extra ->> 'result_code';
    windows_reset := (p_audit_extra ->> 'windows_reset')::BIGINT;
    canonical_response_body := CASE result_code
        WHEN 'no_credit' THEN JSONB_BUILD_OBJECT(
            'result_code', 'no_credit',
            'windows_reset', 0
        )::TEXT
        WHEN 'recovery_deferred' THEN JSONB_BUILD_OBJECT(
            'account_state_recovered', FALSE,
            'recovery_deferred', TRUE,
            'recovery_pending', TRUE,
            'result_code', 'success',
            'windows_reset', windows_reset
        )::TEXT
        WHEN 'recovery_failed' THEN JSONB_BUILD_OBJECT(
            'account_state_recovered', FALSE,
            'recovery_pending', TRUE,
            'result_code', 'success',
            'warning_code', 'OPENAI_AUTO_RESET_RECONCILED_RECOVERY_FAILED',
            'windows_reset', windows_reset
        )::TEXT
        ELSE JSONB_BUILD_OBJECT(
            'account_state_recovered', FALSE,
            'recovery_pending', TRUE,
            'result_code', 'success',
            'windows_reset', windows_reset
        )::TEXT
    END;

    SELECT principal.id
    INTO worker_principal_id
    FROM service_principals AS principal
    WHERE principal.code = 'openai_quota_auto_reset_worker';

    IF worker_principal_id IS NULL THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation worker identity is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    expected_scope :=
        'openai_auto_reset_credit|service_principal:' || worker_principal_id::TEXT;
    expected_path :=
        '/system/openai/accounts/' || p_account_id::TEXT || '/auto-reset-credit';

    IF p_actor_qualified_scope IS DISTINCT FROM expected_scope THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation scope mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT record.*
    INTO stored_record
    FROM idempotency_records AS record
    WHERE record.id = p_idempotency_record_id
    FOR UPDATE;

    IF NOT FOUND
       OR stored_record.scope IS DISTINCT FROM p_actor_qualified_scope
       OR stored_record.request_fingerprint IS DISTINCT FROM p_request_fingerprint THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation record identity mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT protected.account_id,
           protected.protected_at,
           protected.reconciled_at,
           protected.reconciliation_audit_request_id
    INTO stored_account_id,
         stored_protected_at,
         stored_reconciled_at,
         stored_audit_request_id
    FROM openai_quota_auto_reset_protected_attempts AS protected
    WHERE protected.idempotency_record_id = p_idempotency_record_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation marker is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    IF stored_account_id IS DISTINCT FROM p_account_id THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation account provenance mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO audit_logs (
        created_at,
        actor_service_principal_id,
        auth_method,
        action,
        method,
        path,
        request_id,
        status_code,
        extra
    )
    VALUES (
        p_audit_created_at,
        worker_principal_id,
        'service_principal',
        'system.openai.reset_credit.auto',
        'SYSTEM',
        expected_path,
        p_audit_request_id,
        p_audit_status_code,
        p_audit_extra
    )
    ON CONFLICT (action, request_id)
    WHERE action = 'system.openai.reset_credit.auto' AND request_id <> ''
    DO NOTHING;

    SELECT audit.id
    INTO stored_audit_id
    FROM audit_logs AS audit
    WHERE audit.action = 'system.openai.reset_credit.auto'
      AND audit.request_id = p_audit_request_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation audit is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT audit.created_at = p_audit_created_at
           AND audit.actor_user_id IS NULL
           AND audit.actor_service_principal_id = worker_principal_id
           AND audit.actor_email = ''
           AND audit.actor_role = ''
           AND audit.auth_method = 'service_principal'
           AND audit.credential_masked = ''
           AND audit.action = 'system.openai.reset_credit.auto'
           AND audit.method = 'SYSTEM'
           AND audit.path = expected_path
           AND audit.request_id = p_audit_request_id
           AND audit.client_ip = ''
           AND audit.user_agent = ''
           AND audit.request_body = ''
           AND audit.status_code = p_audit_status_code
           AND audit.latency_ms = 0
           AND audit.extra = p_audit_extra
    INTO audit_matches
    FROM audit_logs AS audit
    WHERE audit.id = stored_audit_id;

    IF audit_matches IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'existing OpenAI quota auto-reset reconciliation audit mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    IF stored_reconciled_at IS NOT NULL THEN
        IF stored_audit_request_id IS DISTINCT FROM p_audit_request_id
           OR stored_record.status IS DISTINCT FROM 'succeeded'
           OR stored_record.response_status IS DISTINCT FROM 200
           OR stored_record.response_body IS DISTINCT FROM canonical_response_body
           OR stored_record.error_reason IS NOT NULL
           OR stored_record.locked_until IS NOT NULL
           OR stored_record.expires_at IS DISTINCT FROM
              stored_reconciled_at + INTERVAL '8 days' THEN
            RAISE EXCEPTION 'existing OpenAI quota auto-reset reconciliation outcome mismatch'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN;
    END IF;

    IF stored_record.status IS DISTINCT FROM 'processing'
       OR stored_record.response_status IS NOT NULL
       OR stored_record.response_body IS NOT NULL
       OR stored_record.error_reason IS NOT NULL THEN
        RAISE EXCEPTION 'protected OpenAI quota auto-reset attempt is not reconcilable'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE openai_quota_auto_reset_protected_attempts
    SET reconciled_at = statement_timestamp(),
        reconciliation_audit_request_id = p_audit_request_id
    WHERE idempotency_record_id = p_idempotency_record_id
      AND reconciled_at IS NULL;

    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation marker transition failed'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE idempotency_records
    SET status = 'succeeded',
        response_status = 200,
        response_body = canonical_response_body,
        error_reason = NULL,
        locked_until = NULL,
        expires_at = statement_timestamp() + INTERVAL '8 days',
        updated_at = NOW()
    WHERE id = p_idempotency_record_id
      AND scope = p_actor_qualified_scope
      AND request_fingerprint = p_request_fingerprint
      AND status = 'processing';

    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset reconciliation terminal transition failed'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

ALTER FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    TIMESTAMPTZ,
    TEXT,
    INTEGER,
    JSONB
) OWNER TO CURRENT_USER;

REVOKE ALL ON FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    TIMESTAMPTZ,
    TEXT,
    INTEGER,
    JSONB
) FROM PUBLIC;

-- A no-effect discard is valid only after operators have stopped and drained
-- every old worker and have positively established that the protected upstream
-- call had no effect. It records that decision as a 409 Service Principal audit
-- and removes the marker and parent row atomically, so a previously blocked old
-- UPDATE resumes against a deleted row and cannot recreate or finalize it.
CREATE OR REPLACE FUNCTION discard_openai_quota_auto_reset_protected_attempt_no_effect(
    p_idempotency_record_id BIGINT,
    p_actor_qualified_scope TEXT,
    p_request_fingerprint TEXT,
    p_account_id BIGINT,
    p_audit_created_at TIMESTAMPTZ,
    p_audit_request_id TEXT,
    p_audit_extra JSONB,
    p_old_fleet_drained BOOLEAN
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY INVOKER
AS $$
DECLARE
    stored_record idempotency_records%ROWTYPE;
    stored_account_id BIGINT;
    stored_reconciled_at TIMESTAMPTZ;
    stored_audit_request_id VARCHAR(64);
    worker_principal_id BIGINT;
    expected_scope TEXT;
    expected_path TEXT;
    stored_audit_id BIGINT;
    audit_matches BOOLEAN;
    record_exists BOOLEAN;
    affected_rows INTEGER;
BEGIN
    LOCK TABLE idempotency_records IN ROW EXCLUSIVE MODE;

    IF p_old_fleet_drained IS DISTINCT FROM TRUE
       OR p_idempotency_record_id IS NULL
       OR p_idempotency_record_id <= 0
       OR p_actor_qualified_scope IS NULL
       OR p_request_fingerprint IS NULL
       OR p_account_id IS NULL
       OR p_account_id <= 0
       OR p_audit_created_at IS NULL
       OR p_request_fingerprint !~ '^[0-9a-f]{64}$'
       OR p_audit_request_id IS NULL
       OR LENGTH(p_audit_request_id) NOT BETWEEN 1 AND 64
       OR p_audit_request_id !~ '^[!-~]+$'
       OR p_audit_request_id IS DISTINCT FROM
          ('reconcile-no-effect:' || p_idempotency_record_id::TEXT)
       OR p_audit_extra IS NULL
       OR JSONB_TYPEOF(p_audit_extra) <> 'object'
       OR p_audit_extra -> 'account_id' IS DISTINCT FROM TO_JSONB(p_account_id)
       OR p_audit_extra -> 'idempotency_record_id' IS DISTINCT FROM
          TO_JSONB(p_idempotency_record_id)
       OR JSONB_TYPEOF(p_audit_extra -> 'request_fingerprint')
          IS DISTINCT FROM 'string'
       OR p_audit_extra ->> 'request_fingerprint' IS DISTINCT FROM
          p_request_fingerprint
       OR JSONB_TYPEOF(p_audit_extra -> 'evidence_ref') IS DISTINCT FROM 'string'
       OR LENGTH(BTRIM(p_audit_extra ->> 'evidence_ref')) NOT BETWEEN 1 AND 256
       OR JSONB_TYPEOF(p_audit_extra -> 'decision_owner') IS DISTINCT FROM 'string'
       OR LENGTH(BTRIM(p_audit_extra ->> 'decision_owner')) NOT BETWEEN 1 AND 128
       OR p_audit_extra ->> 'result_code' IS DISTINCT FROM 'reconciled_no_effect'
       OR p_audit_extra -> 'windows_reset' IS DISTINCT FROM TO_JSONB(0) THEN
        RAISE EXCEPTION 'invalid OpenAI quota auto-reset no-effect discard input'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT principal.id
    INTO worker_principal_id
    FROM service_principals AS principal
    WHERE principal.code = 'openai_quota_auto_reset_worker';

    IF worker_principal_id IS NULL THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard worker identity is missing'
            USING ERRCODE = 'check_violation';
    END IF;

    expected_scope :=
        'openai_auto_reset_credit|service_principal:' || worker_principal_id::TEXT;
    expected_path :=
        '/system/openai/accounts/' || p_account_id::TEXT || '/auto-reset-credit';

    IF p_actor_qualified_scope IS DISTINCT FROM expected_scope THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard scope mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT record.*
    INTO stored_record
    FROM idempotency_records AS record
    WHERE record.id = p_idempotency_record_id
    FOR UPDATE;
    record_exists := FOUND;

    IF record_exists THEN
        IF stored_record.scope IS DISTINCT FROM p_actor_qualified_scope
           OR stored_record.request_fingerprint IS DISTINCT FROM p_request_fingerprint
           OR stored_record.status IS DISTINCT FROM 'processing'
           OR stored_record.response_status IS NOT NULL
           OR stored_record.response_body IS NOT NULL
           OR stored_record.error_reason IS NOT NULL THEN
            RAISE EXCEPTION 'protected OpenAI quota auto-reset attempt is not discardable'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT protected.account_id,
               protected.reconciled_at,
               protected.reconciliation_audit_request_id
        INTO stored_account_id,
             stored_reconciled_at,
             stored_audit_request_id
        FROM openai_quota_auto_reset_protected_attempts AS protected
        WHERE protected.idempotency_record_id = p_idempotency_record_id
        FOR UPDATE;

        IF NOT FOUND
           OR stored_reconciled_at IS NOT NULL
           OR stored_audit_request_id IS NOT NULL
           OR stored_account_id IS DISTINCT FROM p_account_id THEN
            RAISE EXCEPTION 'unresolved OpenAI quota auto-reset discard marker is missing'
                USING ERRCODE = 'check_violation';
        END IF;

        INSERT INTO audit_logs (
            created_at,
            actor_service_principal_id,
            auth_method,
            action,
            method,
            path,
            request_id,
            status_code,
            extra
        )
        VALUES (
            p_audit_created_at,
            worker_principal_id,
            'service_principal',
            'system.openai.reset_credit.auto',
            'SYSTEM',
            expected_path,
            p_audit_request_id,
            409,
            p_audit_extra
        )
        ON CONFLICT (action, request_id)
        WHERE action = 'system.openai.reset_credit.auto' AND request_id <> ''
        DO NOTHING;
    END IF;

    SELECT audit.id
    INTO stored_audit_id
    FROM audit_logs AS audit
    WHERE audit.action = 'system.openai.reset_credit.auto'
      AND audit.request_id = p_audit_request_id
    FOR UPDATE;

    IF NOT FOUND THEN
        IF record_exists THEN
            RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard audit is missing'
                USING ERRCODE = 'check_violation';
        END IF;
        RAISE EXCEPTION
            'OpenAI quota auto-reset discard record is missing and exact audit was not found'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT audit.created_at = p_audit_created_at
           AND audit.actor_user_id IS NULL
           AND audit.actor_service_principal_id = worker_principal_id
           AND audit.actor_email = ''
           AND audit.actor_role = ''
           AND audit.auth_method = 'service_principal'
           AND audit.credential_masked = ''
           AND audit.action = 'system.openai.reset_credit.auto'
           AND audit.method = 'SYSTEM'
           AND audit.path = expected_path
           AND audit.request_id = p_audit_request_id
           AND audit.client_ip = ''
           AND audit.user_agent = ''
           AND audit.request_body = ''
           AND audit.status_code = 409
           AND audit.latency_ms = 0
           AND audit.extra = p_audit_extra
    INTO audit_matches
    FROM audit_logs AS audit
    WHERE audit.id = stored_audit_id;

    IF audit_matches IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'existing OpenAI quota auto-reset no-effect discard audit mismatch'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT record_exists THEN
        RETURN;
    END IF;

    -- Clear only the still-current recovery state for this exact protected
    -- attempt. A missing account or a newer/different state is intentionally
    -- untouched. The hash comparison avoids exposing either upstream credit ID
    -- while making a no-effect decision actually reclaimable by the new fleet.
    UPDATE accounts AS account
    SET extra = account.extra - 'codex_auto_reset_credit_state',
        updated_at = NOW()
    WHERE account.id = p_account_id
      AND account.extra -> 'codex_auto_reset_credit_state' ->> 'status'
          IN ('resetting', 'failed')
      AND account.extra -> 'codex_auto_reset_credit_state' ->>
          'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
      AND account.extra -> 'codex_auto_reset_credit_state' ->>
          'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
      AND stored_record.idempotency_key_hash = ENCODE(
          SHA256(CONVERT_TO(
              'oarc:' || account.id::TEXT || ':' ||
              (account.extra -> 'codex_auto_reset_credit_state' ->>
                  'attempt_credit_hash') || ':' ||
              (account.extra -> 'codex_auto_reset_credit_state' ->>
                  'attempt_cycle_hash'),
              'UTF8'
          )),
          'hex'
      );

    DELETE FROM openai_quota_auto_reset_protected_attempts
    WHERE idempotency_record_id = p_idempotency_record_id
      AND reconciled_at IS NULL
      AND reconciliation_audit_request_id IS NULL;

    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard marker delete failed'
            USING ERRCODE = 'check_violation';
    END IF;

    -- The generic expiry guard intentionally skips future rows. Expire this row
    -- only inside the discard transaction after removing its still-uncommitted
    -- marker, then require the parent delete to affect exactly one row.
    UPDATE idempotency_records
    SET status = 'failed_retryable',
        response_status = NULL,
        response_body = NULL,
        error_reason = 'RECONCILED_NO_EFFECT',
        locked_until = NULL,
        expires_at = '-infinity'::TIMESTAMPTZ,
        updated_at = NOW()
    WHERE id = p_idempotency_record_id
      AND scope = p_actor_qualified_scope
      AND request_fingerprint = p_request_fingerprint
      AND status = 'processing';

    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard expiry transition failed'
            USING ERRCODE = 'check_violation';
    END IF;

    DELETE FROM idempotency_records
    WHERE id = p_idempotency_record_id
      AND scope = p_actor_qualified_scope
      AND request_fingerprint = p_request_fingerprint
      AND status = 'failed_retryable'
      AND error_reason = 'RECONCILED_NO_EFFECT';

    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'OpenAI quota auto-reset no-effect discard parent delete failed'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

ALTER FUNCTION discard_openai_quota_auto_reset_protected_attempt_no_effect(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    TIMESTAMPTZ,
    TEXT,
    JSONB,
    BOOLEAN
) OWNER TO CURRENT_USER;

REVOKE ALL ON FUNCTION discard_openai_quota_auto_reset_protected_attempt_no_effect(
    BIGINT,
    TEXT,
    TEXT,
    BIGINT,
    TIMESTAMPTZ,
    TEXT,
    JSONB,
    BOOLEAN
) FROM PUBLIC;

-- CREATE OR REPLACE preserves an existing ACL, while CREATE may inherit
-- explicit default grants to application roles. Revoke every non-owner grant
-- after setting a deterministic owner, then verify both controlled entry points
-- remain invoker-rights and owner-only on every raw reapplication.
DO $$
DECLARE
    procedure_contract RECORD;
    procedure_oid OID;
    granted_role NAME;
BEGIN
    FOR procedure_contract IN
        SELECT contract.proname, contract.expected_oid
        FROM (
            VALUES
                (
                    'reconcile_openai_quota_auto_reset_protected_attempt'::NAME,
                    'reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)'::REGPROCEDURE::OID
                ),
                (
                    'discard_openai_quota_auto_reset_protected_attempt_no_effect'::NAME,
                    'discard_openai_quota_auto_reset_protected_attempt_no_effect(bigint,text,text,bigint,timestamptz,text,jsonb,boolean)'::REGPROCEDURE::OID
                )
        ) AS contract(proname, expected_oid)
    LOOP
        procedure_oid := procedure_contract.expected_oid;

        IF (
            SELECT COUNT(*)
            FROM pg_proc AS procedure
            WHERE procedure.pronamespace = CURRENT_SCHEMA()::REGNAMESPACE
              AND procedure.proname = procedure_contract.proname
        ) <> 1
        OR NOT EXISTS (
            SELECT 1
            FROM pg_proc AS procedure
            WHERE procedure.oid = procedure_oid
              AND procedure.pronamespace = CURRENT_SCHEMA()::REGNAMESPACE
              AND procedure.proname = procedure_contract.proname
              AND procedure.prokind = 'f'
              AND procedure.prorettype = 'void'::REGTYPE
        ) THEN
            RAISE EXCEPTION
                'unsafe OpenAI quota auto-reset reconciliation function overload';
        END IF;

        FOR granted_role IN
            SELECT DISTINCT role_row.rolname
            FROM pg_proc AS procedure
            CROSS JOIN LATERAL ACLEXPLODE(
                COALESCE(procedure.proacl, ACLDEFAULT('f', procedure.proowner))
            ) AS privilege
            JOIN pg_roles AS role_row
              ON role_row.oid = privilege.grantee
            WHERE procedure.oid = procedure_oid
              AND privilege.privilege_type = 'EXECUTE'
              AND privilege.grantee <> procedure.proowner
        LOOP
            EXECUTE FORMAT(
                'REVOKE ALL ON FUNCTION %s FROM %I',
                procedure_oid::REGPROCEDURE,
                granted_role
            );
        END LOOP;

        IF EXISTS (
            SELECT 1
            FROM pg_proc AS procedure
            WHERE procedure.oid = procedure_oid
              AND (
                  procedure.proowner <> (
                      SELECT role_row.oid
                      FROM pg_roles AS role_row
                      WHERE role_row.rolname = CURRENT_USER
                  )
                  OR procedure.prosecdef
                  OR EXISTS (
                      SELECT 1
                      FROM ACLEXPLODE(
                          COALESCE(
                              procedure.proacl,
                              ACLDEFAULT('f', procedure.proowner)
                          )
                      ) AS privilege
                      WHERE privilege.privilege_type = 'EXECUTE'
                        AND privilege.grantee <> procedure.proowner
                  )
              )
        ) THEN
            RAISE EXCEPTION
                'OpenAI quota auto-reset reconciliation function is not owner-only';
        END IF;
    END LOOP;
END
$$;

-- Old binaries qualify this operation with account:<id> and perform the
-- external reset only after inserting or reclaiming the corresponding durable
-- idempotency row. Rejecting every future account-qualified scope therefore
-- makes mixed-version execution and rollback fail before the upstream effect.
-- The table lock above closes the write window between migration and fence
-- installation; canonical Service Principal and raw upgrade-fence scopes stay
-- valid.
CREATE OR REPLACE FUNCTION reject_legacy_openai_auto_reset_account_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.scope ~ '^openai_auto_reset_credit\|account:[1-9][0-9]*$' THEN
        RAISE EXCEPTION
            'legacy OpenAI quota auto-reset account scope is fenced: %', NEW.scope
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS idempotency_records_openai_auto_reset_account_scope_fence
    ON idempotency_records;
CREATE TRIGGER idempotency_records_openai_auto_reset_account_scope_fence
BEFORE INSERT OR UPDATE OF scope
ON idempotency_records
FOR EACH ROW
EXECUTE FUNCTION reject_legacy_openai_auto_reset_account_scope();

-- An expired processing record for this external-effecting worker is an
-- ambiguous outcome, not disposable cache. A succeeded Worker record is also
-- durable recovery input while an account with the same stable key remains in
-- resetting/failed state. Preserve both while retaining migration 236's
-- delete-time renewed-expiry recheck for all idempotency rows.
CREATE OR REPLACE FUNCTION guard_idempotency_record_expiry_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.expires_at > CURRENT_TIMESTAMP THEN
        RETURN NULL;
    END IF;
    IF OLD.status = 'processing'
       AND (
           OLD.scope ~ '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
           OR (
               OLD.scope = 'openai_auto_reset_credit'
               AND OLD.request_fingerprint <> 'upgrade-fence:actor-qualified:v1'
               AND OLD.request_fingerprint ~ '^[0-9a-f]{64}$'
           )
       ) THEN
        RETURN NULL;
    END IF;
    IF OLD.status = 'succeeded'
       AND EXISTS (
           SELECT 1
           FROM service_principals AS principal
           JOIN accounts AS account ON TRUE
           WHERE principal.code = 'openai_quota_auto_reset_worker'
             AND OLD.scope =
                 'openai_auto_reset_credit|service_principal:' ||
                 principal.id::TEXT
             AND account.extra -> 'codex_auto_reset_credit_state' ->>
                 'status' IN ('resetting', 'failed')
             AND account.extra -> 'codex_auto_reset_credit_state' ->>
                 'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
             AND account.extra -> 'codex_auto_reset_credit_state' ->>
                 'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
             AND OLD.idempotency_key_hash = ENCODE(
                 SHA256(CONVERT_TO(
                     'oarc:' || account.id::TEXT || ':' ||
                     (account.extra -> 'codex_auto_reset_credit_state' ->>
                         'attempt_credit_hash') || ':' ||
                     (account.extra -> 'codex_auto_reset_credit_state' ->>
                         'attempt_cycle_hash'),
                     'UTF8'
                 )),
                 'hex'
             )
       ) THEN
        RETURN NULL;
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS idempotency_records_expiry_delete_guard
    ON idempotency_records;
CREATE TRIGGER idempotency_records_expiry_delete_guard
BEFORE DELETE
ON idempotency_records
FOR EACH ROW
EXECUTE FUNCTION guard_idempotency_record_expiry_delete();
