-- Read-only Phase 0 preflight. Run against a read replica or a transaction
-- configured as READ ONLY. This script emits counts and identifiers only; it
-- never returns account credentials, API keys, or other secret values.

BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;

SELECT role, COUNT(*) AS user_count
FROM users
GROUP BY role
ORDER BY role;

SELECT COUNT(*) AS users_with_unexpected_role
FROM users
WHERE role IS NULL OR role NOT IN ('admin', 'user');

SELECT lower(name) AS folded_name, COUNT(*) AS active_group_count,
       array_agg(id ORDER BY id) AS group_ids
FROM groups
WHERE deleted_at IS NULL
GROUP BY lower(name)
HAVING COUNT(*) > 1
ORDER BY active_group_count DESC, folded_name;

SELECT lower(name) AS folded_name, COUNT(*) AS active_account_count,
       array_agg(id ORDER BY id) AS account_ids
FROM accounts
WHERE deleted_at IS NULL
GROUP BY lower(name)
HAVING COUNT(*) > 1
ORDER BY active_account_count DESC, folded_name;

SELECT uag.user_id, uag.group_id
FROM user_allowed_groups AS uag
LEFT JOIN users AS u ON u.id = uag.user_id
LEFT JOIN groups AS g ON g.id = uag.group_id
WHERE u.id IS NULL OR g.id IS NULL OR u.deleted_at IS NOT NULL OR g.deleted_at IS NOT NULL
ORDER BY uag.user_id, uag.group_id;

SELECT ag.account_id, ag.group_id
FROM account_groups AS ag
LEFT JOIN accounts AS a ON a.id = ag.account_id
LEFT JOIN groups AS g ON g.id = ag.group_id
WHERE a.id IS NULL OR g.id IS NULL OR a.deleted_at IS NOT NULL OR g.deleted_at IS NOT NULL
ORDER BY ag.account_id, ag.group_id;

SELECT lower(name) AS default_name, COUNT(*) AS active_group_count,
       array_agg(id ORDER BY id) AS group_ids
FROM groups
WHERE deleted_at IS NULL
  AND lower(name) LIKE '%-default'
GROUP BY lower(name)
HAVING COUNT(*) > 1
ORDER BY default_name;

SELECT
  (SELECT COUNT(*) FROM users) AS users_total,
  (SELECT COUNT(*) FROM accounts WHERE deleted_at IS NULL) AS active_accounts,
  (SELECT COUNT(*) FROM groups WHERE deleted_at IS NULL) AS active_groups,
  (SELECT COUNT(*) FROM user_allowed_groups) AS legacy_group_grants,
  (SELECT COUNT(*) FROM account_groups) AS account_group_links,
  (SELECT COUNT(*) FROM api_keys WHERE deleted_at IS NULL) AS active_api_keys;

-- Migration 243 must retain an account identity for every ambiguous OpenAI
-- quota auto-reset attempt. Account-qualified scope carries that identity
-- directly. Older raw/SP scope is resolvable only when its durable key hash
-- matches exactly one account's persisted pending state. When that exact state
-- still exists, the stored fingerprint must also match either the legacy
-- account Actor or the reserved Worker Actor accepted by runtime recovery.
-- Archive this result immediately before the maintenance-window migration;
-- every row must be "resolved". This inventory emits database identifiers and
-- categories only, never hashes, fingerprints, state values, or credentials.
WITH worker_identity AS (
  SELECT MIN(id) AS worker_principal_id
  FROM service_principals
  WHERE code = 'openai_quota_auto_reset_worker'
), auto_reset_candidate_base AS (
  SELECT
    record.id AS idempotency_record_id,
    record.status,
    record.idempotency_key_hash,
    record.request_fingerprint,
    CASE
      WHEN LEFT(record.scope, LENGTH('openai_auto_reset_credit|account:')) =
        'openai_auto_reset_credit|account:'
        THEN 'account'
      WHEN LEFT(
        record.scope,
        LENGTH('openai_auto_reset_credit|service_principal:')
      ) = 'openai_auto_reset_credit|service_principal:'
        THEN 'service_principal'
      ELSE 'raw'
    END AS scope_kind,
    SUBSTRING(
      record.scope FROM
      '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
    ) AS scope_account_digits,
    SUBSTRING(
      record.scope FROM
      '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
    ) AS scope_principal_digits
  FROM idempotency_records AS record
  WHERE record.status IN ('processing', 'failed_retryable')
    AND (
      LEFT(record.scope, LENGTH('openai_auto_reset_credit|account:')) =
        'openai_auto_reset_credit|account:'
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
), auto_reset_candidates AS (
  SELECT
    candidate.*,
    CASE
      WHEN candidate.scope_kind = 'account'
        AND candidate.scope_account_digits IS NOT NULL
        AND (
          LENGTH(candidate.scope_account_digits) < 19
          OR (
            LENGTH(candidate.scope_account_digits) = 19
            AND candidate.scope_account_digits COLLATE "C" <=
              '9223372036854775807'
          )
        )
        THEN candidate.scope_account_digits::BIGINT
      ELSE NULL
    END AS scope_account_id,
    CASE candidate.scope_kind
      WHEN 'account' THEN
        candidate.scope_account_digits IS NOT NULL
        AND (
          LENGTH(candidate.scope_account_digits) < 19
          OR (
            LENGTH(candidate.scope_account_digits) = 19
            AND candidate.scope_account_digits COLLATE "C" <=
              '9223372036854775807'
          )
        )
      WHEN 'service_principal' THEN
        candidate.scope_principal_digits IS NOT NULL
        AND (
          LENGTH(candidate.scope_principal_digits) < 19
          OR (
            LENGTH(candidate.scope_principal_digits) = 19
            AND candidate.scope_principal_digits COLLATE "C" <=
              '9223372036854775807'
          )
        )
      ELSE TRUE
    END AS scope_identity_is_canonical
  FROM auto_reset_candidate_base AS candidate
), matching_pending_states AS (
  SELECT
    candidate.idempotency_record_id,
    account.id AS account_id,
    (
      candidate.request_fingerprint = ENCODE(
        SHA256(CONVERT_TO(
          'POST' || CHR(10) ||
          '/system/openai/reset-credit/auto' || CHR(10) ||
          'account:' || account.id::TEXT || CHR(10) ||
          payload.canonical_payload,
          'UTF8'
        )),
        'hex'
      )
      OR (
        worker.worker_principal_id IS NOT NULL
        AND candidate.request_fingerprint = ENCODE(
          SHA256(CONVERT_TO(
            'POST' || CHR(10) ||
            '/system/openai/reset-credit/auto' || CHR(10) ||
            'service_principal:' || worker.worker_principal_id::TEXT || CHR(10) ||
            payload.canonical_payload,
            'UTF8'
          )),
          'hex'
        )
      )
    ) AS fingerprint_matches
  FROM auto_reset_candidates AS candidate
  JOIN accounts AS account
    ON (
      (
        candidate.scope_kind = 'account'
        AND account.id = candidate.scope_account_id
      )
      OR (
        candidate.scope_kind <> 'account'
        AND account.platform = 'openai'
      )
    )
   AND account.extra -> 'codex_auto_reset_credit_state' ->> 'status'
       IN ('resetting', 'failed')
   AND account.extra -> 'codex_auto_reset_credit_state' ->>
       'attempt_credit_hash' ~ '^[0-9a-f]{24}$'
   AND account.extra -> 'codex_auto_reset_credit_state' ->>
       'attempt_cycle_hash' ~ '^[0-9a-f]{24}$'
   AND candidate.idempotency_key_hash = ENCODE(
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
  CROSS JOIN worker_identity AS worker
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
), pending_state_summary AS (
  SELECT
    candidate.idempotency_record_id,
    COALESCE(
      ARRAY_AGG(state.account_id ORDER BY state.account_id)
        FILTER (WHERE state.account_id IS NOT NULL),
      ARRAY[]::BIGINT[]
    ) AS mapped_account_ids,
    COUNT(state.account_id) AS matching_state_count,
    COALESCE(BOOL_OR(state.fingerprint_matches), FALSE) AS fingerprint_matches
  FROM auto_reset_candidates AS candidate
  LEFT JOIN matching_pending_states AS state
    ON state.idempotency_record_id = candidate.idempotency_record_id
  GROUP BY candidate.idempotency_record_id
), auto_reset_inventory AS (
  SELECT
    candidate.idempotency_record_id,
    candidate.status,
    candidate.scope_kind,
    CASE
      WHEN candidate.scope_kind = 'account'
        AND candidate.scope_account_id IS NOT NULL
        THEN ARRAY[candidate.scope_account_id]
      WHEN candidate.scope_kind = 'account'
        THEN ARRAY[]::BIGINT[]
      ELSE summary.mapped_account_ids
    END AS account_ids,
    CASE
      WHEN candidate.idempotency_key_hash IS NULL
        OR candidate.idempotency_key_hash !~ '^[0-9a-f]{64}$'
        OR candidate.request_fingerprint IS NULL
        OR candidate.request_fingerprint !~ '^[0-9a-f]{64}$'
        OR NOT candidate.scope_identity_is_canonical
        THEN 'malformed_identity'
      WHEN candidate.scope_kind <> 'account'
        AND summary.matching_state_count = 0
        THEN 'unmatched'
      WHEN candidate.scope_kind <> 'account'
        AND summary.matching_state_count > 1
        THEN 'ambiguous'
      WHEN summary.matching_state_count = 1
        AND NOT summary.fingerprint_matches
        THEN 'recovery_fingerprint_mismatch'
      ELSE 'resolved'
    END AS provenance_state
  FROM auto_reset_candidates AS candidate
  JOIN pending_state_summary AS summary
    ON summary.idempotency_record_id = candidate.idempotency_record_id
), normalization_sources AS (
  -- Migration 243 rewrites every canonical account scope, regardless of
  -- terminal status. It also rewrites raw/non-worker SP rows only after they
  -- have passed the provenance checks above and would receive a protection
  -- marker. Carry the key hash only inside this read-only CTE; it is never
  -- emitted by the inventory.
  SELECT
    record.id AS idempotency_record_id,
    record.idempotency_key_hash
  FROM idempotency_records AS record
  WHERE record.scope ~
    '^openai_auto_reset_credit\|account:[1-9][0-9]*$'

  UNION

  SELECT
    candidate.idempotency_record_id,
    candidate.idempotency_key_hash
  FROM auto_reset_candidates AS candidate
  JOIN auto_reset_inventory AS inventory
    ON inventory.idempotency_record_id = candidate.idempotency_record_id
  WHERE candidate.scope_kind <> 'account'
    AND inventory.provenance_state = 'resolved'
), normalization_participants AS (
  SELECT
    source.idempotency_record_id,
    source.idempotency_key_hash
  FROM normalization_sources AS source

  UNION

  -- On a migration reapplication, an existing row may already occupy the
  -- reserved Worker's target scope. It participates in the same uniqueness
  -- check even though its own scope would not change.
  SELECT
    record.id AS idempotency_record_id,
    record.idempotency_key_hash
  FROM idempotency_records AS record
  CROSS JOIN worker_identity AS worker
  WHERE worker.worker_principal_id IS NOT NULL
    AND record.scope =
      'openai_auto_reset_credit|service_principal:' ||
      worker.worker_principal_id::TEXT
), normalization_collision_keys AS (
  SELECT participant.idempotency_key_hash
  FROM normalization_participants AS participant
  GROUP BY participant.idempotency_key_hash
  HAVING COUNT(DISTINCT participant.idempotency_record_id) > 1
), normalization_collision_ids AS (
  SELECT DISTINCT participant.idempotency_record_id
  FROM normalization_participants AS participant
  JOIN normalization_collision_keys AS collision
    ON collision.idempotency_key_hash = participant.idempotency_key_hash
), classified_inventory AS (
  SELECT
    inventory.idempotency_record_id,
    inventory.status,
    inventory.scope_kind,
    inventory.account_ids,
    CASE
      WHEN inventory.provenance_state = 'resolved'
        AND collision.idempotency_record_id IS NOT NULL
        THEN 'target_scope_collision'
      ELSE inventory.provenance_state
    END AS provenance_state
  FROM auto_reset_inventory AS inventory
  LEFT JOIN normalization_collision_ids AS collision
    ON collision.idempotency_record_id = inventory.idempotency_record_id
), collision_only_inventory AS (
  -- Terminal account rows and pre-existing target rows were not part of the
  -- in-flight provenance inventory, but they must still be visible when they
  -- would make the scope rewrite violate (scope, idempotency_key_hash).
  SELECT
    record.id AS idempotency_record_id,
    record.status,
    CASE
      WHEN LEFT(record.scope, LENGTH('openai_auto_reset_credit|account:')) =
        'openai_auto_reset_credit|account:'
        THEN 'account'
      WHEN LEFT(
        record.scope,
        LENGTH('openai_auto_reset_credit|service_principal:')
      ) = 'openai_auto_reset_credit|service_principal:'
        THEN 'service_principal'
      ELSE 'raw'
    END AS scope_kind,
    CASE
      WHEN record.scope ~
        '^openai_auto_reset_credit\|account:[1-9][0-9]*$'
        AND (
          LENGTH(SUBSTRING(
            record.scope FROM
            '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
          )) < 19
          OR (
            LENGTH(SUBSTRING(
              record.scope FROM
              '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
            )) = 19
            AND SUBSTRING(
              record.scope FROM
              '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
            ) COLLATE "C" <= '9223372036854775807'
          )
        )
        THEN ARRAY[SUBSTRING(
          record.scope FROM
          '^openai_auto_reset_credit\|account:([1-9][0-9]*)$'
        )::BIGINT]
      ELSE ARRAY[]::BIGINT[]
    END AS account_ids,
    'target_scope_collision' AS provenance_state
  FROM normalization_collision_ids AS collision
  JOIN idempotency_records AS record
    ON record.id = collision.idempotency_record_id
  LEFT JOIN auto_reset_inventory AS inventory
    ON inventory.idempotency_record_id = record.id
  WHERE inventory.idempotency_record_id IS NULL
), final_inventory AS (
  SELECT * FROM classified_inventory
  UNION ALL
  SELECT * FROM collision_only_inventory
)
SELECT
  idempotency_record_id,
  status,
  scope_kind,
  account_ids,
  provenance_state
FROM final_inventory
ORDER BY idempotency_record_id;

-- A succeeded idempotency row can still leave an account in resetting/failed
-- when an older binary persisted a response that the hardened replay decoder
-- cannot consume. In particular, the generic response redactor replaced the
-- legacy `code` value with `***`. Migration 243 does not repair or reconcile
-- these terminal rows, so every row emitted below blocks the migration.
--
-- This is intentionally a separate inventory from the five-column in-flight
-- provenance result above. It emits only database identifiers and a bounded
-- classification; response bodies, stable-key hashes, attempt hashes, and
-- request fingerprints never leave the query.
WITH terminal_worker_identity AS (
  SELECT MIN(id) AS worker_principal_id
  FROM service_principals
  WHERE code = 'openai_quota_auto_reset_worker'
), terminal_managed_accounts AS (
  SELECT
    account.id AS account_id,
    (
      account.deleted_at IS NULL
      AND account.platform = 'openai'
      AND account.type = 'oauth'
      AND account.parent_account_id IS NULL
    ) AS account_is_reachable,
    managed.state_document
  FROM accounts AS account
  CROSS JOIN LATERAL (
    SELECT account.extra ->
      'codex_auto_reset_credit_state' AS state_document
  ) AS managed
  WHERE account.extra ? 'codex_auto_reset_credit_state'
    AND managed.state_document <> 'null'::JSONB
), terminal_managed_account_fields AS (
  SELECT
    account.*,
    account.state_document ->> 'status' AS pending_status,
    account.state_document ->> 'attempt_credit_hash' AS attempt_credit_hash,
    account.state_document ->> 'attempt_cycle_hash' AS attempt_cycle_hash
  FROM terminal_managed_accounts AS account
), terminal_managed_account_validation AS (
  SELECT
    account.*,
    COALESCE((
      JSONB_TYPEOF(account.state_document) = 'object'
      AND JSONB_TYPEOF(account.state_document -> 'status') = 'string'
      AND account.pending_status IN (
        'checking',
        'available',
        'resetting',
        'success',
        'no_credit',
        'failed'
      )
      AND NOT EXISTS (
        SELECT 1
        FROM JSONB_OBJECT_KEYS(
          CASE
            WHEN JSONB_TYPEOF(account.state_document) = 'object'
              THEN account.state_document
            ELSE '{}'::JSONB
          END
        ) AS field(name)
        WHERE field.name NOT IN (
          'status',
          'trigger_window',
          'available_count',
          'checked_at',
          'last_result_at',
          'error_code',
          'attempt_cycle_hash',
          'attempt_credit_hash'
        )
      )
      AND (
        NOT (account.state_document ? 'trigger_window')
        OR account.state_document -> 'trigger_window' = 'null'::JSONB
        OR JSONB_TYPEOF(account.state_document -> 'trigger_window') = 'string'
      )
      AND (
        NOT (account.state_document ? 'checked_at')
        OR account.state_document -> 'checked_at' = 'null'::JSONB
        OR JSONB_TYPEOF(account.state_document -> 'checked_at') = 'string'
      )
      AND (
        NOT (account.state_document ? 'last_result_at')
        OR account.state_document -> 'last_result_at' = 'null'::JSONB
        OR JSONB_TYPEOF(account.state_document -> 'last_result_at') = 'string'
      )
      AND (
        NOT (account.state_document ? 'error_code')
        OR account.state_document -> 'error_code' = 'null'::JSONB
        OR JSONB_TYPEOF(account.state_document -> 'error_code') = 'string'
      )
      AND (
        NOT (account.state_document ? 'attempt_credit_hash')
        OR account.state_document -> 'attempt_credit_hash' = 'null'::JSONB
        OR JSONB_TYPEOF(
          account.state_document -> 'attempt_credit_hash'
        ) = 'string'
      )
      AND (
        NOT (account.state_document ? 'attempt_cycle_hash')
        OR account.state_document -> 'attempt_cycle_hash' = 'null'::JSONB
        OR JSONB_TYPEOF(
          account.state_document -> 'attempt_cycle_hash'
        ) = 'string'
      )
      AND (
        NOT (account.state_document ? 'available_count')
        OR account.state_document -> 'available_count' = 'null'::JSONB
        OR CASE
          WHEN JSONB_TYPEOF(
            account.state_document -> 'available_count'
          ) = 'number'
          AND account.state_document ->>
            'available_count' ~
              '^(0|[1-9][0-9]{0,9})$'
          THEN (account.state_document ->>
            'available_count')::NUMERIC BETWEEN
              0 AND 2147483647
          ELSE FALSE
        END
      )
      AND (
        (
          COALESCE(account.attempt_credit_hash, '') = ''
          AND COALESCE(account.attempt_cycle_hash, '') = ''
        )
        OR (
          account.attempt_credit_hash ~ '^[0-9a-f]{24}$'
          AND account.attempt_cycle_hash ~ '^[0-9a-f]{24}$'
        )
      )
      AND (
        account.pending_status <> 'resetting'
        OR (
          account.attempt_credit_hash ~ '^[0-9a-f]{24}$'
          AND account.attempt_cycle_hash ~ '^[0-9a-f]{24}$'
        )
      )
    ), FALSE) AS managed_state_is_decodable,
    COALESCE((
      account.pending_status = 'resetting'
      OR (
        account.pending_status = 'failed'
        AND (
          COALESCE(account.attempt_credit_hash, '') <> ''
          OR COALESCE(account.attempt_cycle_hash, '') <> ''
        )
      )
    ), FALSE) AS terminal_recovery_is_required
  FROM terminal_managed_account_fields AS account
), terminal_account_inventory AS (
  -- The recovery pager/GetByID path cannot reach structurally invalid accounts.
  -- The runtime parser also rejects malformed managed state before any reset.
  -- A failed state with no attempt hashes can be a normal pre-effect query
  -- failure; every malformed state or unreachable durable attempt blocks.
  SELECT
    NULL::BIGINT AS idempotency_record_id,
    account.account_id,
    CASE
      WHEN NOT account.managed_state_is_decodable
        THEN 'malformed_pending_state'
      ELSE 'unreachable_account'
    END AS response_state
  FROM terminal_managed_account_validation AS account
  WHERE NOT account.managed_state_is_decodable
    OR (
      account.terminal_recovery_is_required
      AND NOT account.account_is_reachable
    )
), terminal_pending_states AS (
  SELECT
    account.account_id,
    ENCODE(
      SHA256(CONVERT_TO(
        'oarc:' || account.account_id::TEXT || ':' ||
        account.attempt_credit_hash || ':' ||
        account.attempt_cycle_hash,
        'UTF8'
      )),
      'hex'
    ) AS stable_key_hash,
    ENCODE(
      SHA256(CONVERT_TO(
        'POST' || CHR(10) ||
        '/system/openai/reset-credit/auto' || CHR(10) ||
        'account:' || account.account_id::TEXT || CHR(10) ||
        payload.canonical_payload,
        'UTF8'
      )),
      'hex'
    ) AS legacy_fingerprint,
    CASE
      WHEN worker.worker_principal_id IS NOT NULL THEN ENCODE(
        SHA256(CONVERT_TO(
          'POST' || CHR(10) ||
          '/system/openai/reset-credit/auto' || CHR(10) ||
          'service_principal:' || worker.worker_principal_id::TEXT || CHR(10) ||
          payload.canonical_payload,
          'UTF8'
        )),
        'hex'
      )
      ELSE NULL
    END AS current_fingerprint,
    worker.worker_principal_id
  FROM terminal_managed_account_validation AS account
  CROSS JOIN terminal_worker_identity AS worker
  CROSS JOIN LATERAL (
    SELECT
      '{"account_id":' || account.account_id::TEXT ||
      ',"credit_hash":"' ||
      account.attempt_credit_hash ||
      '","cycle_hash":"' ||
      account.attempt_cycle_hash || '"}' AS canonical_payload
  ) AS payload
  WHERE account.account_is_reachable
    AND account.managed_state_is_decodable
    AND account.terminal_recovery_is_required
), terminal_candidates AS (
  SELECT
    record.id AS idempotency_record_id,
    state.account_id,
    record.scope,
    record.request_fingerprint,
    record.response_status,
    record.response_body,
    state.legacy_fingerprint,
    state.current_fingerprint,
    state.worker_principal_id,
    (
      record.scope =
        'openai_auto_reset_credit|account:' || state.account_id::TEXT
      OR (
        state.worker_principal_id IS NOT NULL
        AND record.scope =
          'openai_auto_reset_credit|service_principal:' ||
          state.worker_principal_id::TEXT
      )
    ) AS scope_is_reachable,
    (
      record.request_fingerprint = state.legacy_fingerprint
      OR record.request_fingerprint = state.current_fingerprint
    ) AS fingerprint_is_reachable
  FROM terminal_pending_states AS state
  JOIN idempotency_records AS record
    ON record.status = 'succeeded'
   AND record.idempotency_key_hash = state.stable_key_hash
   AND (
     record.scope = 'openai_auto_reset_credit'
     OR record.scope ~
       '^openai_auto_reset_credit\|account:[1-9][0-9]*$'
     OR record.scope ~
       '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
   )
), terminal_inflight_coverage AS (
  -- A pending account is also covered when migration 243 can protect an exact
  -- non-terminal executable parent. Raw upgrade fences are not executable and
  -- therefore do not satisfy this coverage check.
  SELECT DISTINCT state.account_id
  FROM terminal_pending_states AS state
  JOIN idempotency_records AS record
    ON record.status IN ('processing', 'failed_retryable')
   AND record.idempotency_key_hash = state.stable_key_hash
   AND (
     record.request_fingerprint = state.legacy_fingerprint
     OR record.request_fingerprint = state.current_fingerprint
   )
   AND (
     record.scope =
       'openai_auto_reset_credit|account:' || state.account_id::TEXT
     OR record.scope = 'openai_auto_reset_credit'
     OR (
       record.scope ~
         '^openai_auto_reset_credit\|service_principal:[1-9][0-9]*$'
       AND (
         LENGTH(SUBSTRING(
           record.scope FROM
           '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
         )) < 19
         OR (
           LENGTH(SUBSTRING(
             record.scope FROM
             '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
           )) = 19
           AND SUBSTRING(
             record.scope FROM
             '^openai_auto_reset_credit\|service_principal:([1-9][0-9]*)$'
           ) COLLATE "C" <= '9223372036854775807'
         )
       )
     )
   )
), terminal_attempt_coverage AS (
  SELECT account_id FROM terminal_inflight_coverage
  UNION
  SELECT account_id FROM terminal_candidates
), terminal_missing_attempts AS (
  SELECT state.account_id
  FROM terminal_pending_states AS state
  LEFT JOIN terminal_attempt_coverage AS coverage
    ON coverage.account_id = state.account_id
  WHERE coverage.account_id IS NULL
), terminal_response_documents AS (
  SELECT
    candidate.*,
    CASE
      -- PostgreSQL 15 has no fail-soft JSON cast. Durable auto-reset responses
      -- are flat objects containing only these scalar token classes, so this
      -- lexical gate makes the following JSONB cast safe without PG16 helpers.
      WHEN candidate.response_body ~ $terminal_json$^[[:space:]]*\{[[:space:]]*("[a-z_]+"[[:space:]]*:[[:space:]]*("[A-Za-z0-9_*.:+_-]*"|-?(0|[1-9][0-9]{0,10})|true|false|null)([[:space:]]*,[[:space:]]*"[a-z_]+"[[:space:]]*:[[:space:]]*("[A-Za-z0-9_*.:+_-]*"|-?(0|[1-9][0-9]{0,10})|true|false|null))*)?[[:space:]]*\}[[:space:]]*$terminal_json$
        THEN candidate.response_body::JSONB
      ELSE NULL
    END AS response_document
  FROM terminal_candidates AS candidate
), terminal_response_shapes AS (
  SELECT
    document.*,
    CASE
      WHEN document.response_document IS NULL THEN FALSE
      WHEN EXISTS (
        SELECT 1
        FROM JSONB_OBJECT_KEYS(document.response_document) AS field(name)
        WHERE field.name NOT IN (
          'result_code',
          'code',
          'windows_reset',
          'available_count',
          'available_count_known',
          'post_process_recorded',
          'recovery_pending',
          'recovery_deferred',
          'account_state_recovered',
          'warning_code'
        )
      ) THEN FALSE
      WHEN NOT (document.response_document ? 'windows_reset')
        OR JSONB_TYPEOF(document.response_document -> 'windows_reset') <>
          'number'
        OR document.response_document ->> 'windows_reset' !~
          '^-?(0|[1-9][0-9]*)$'
        THEN FALSE
      WHEN NOT (
        document.response_document ? 'result_code'
        OR document.response_document ? 'code'
      ) THEN FALSE
      WHEN document.response_document ? 'result_code'
        AND JSONB_TYPEOF(document.response_document -> 'result_code') <>
          'string'
        THEN FALSE
      WHEN document.response_document ? 'code'
        AND JSONB_TYPEOF(document.response_document -> 'code') <> 'string'
        THEN FALSE
      WHEN document.response_document ? 'available_count'
        AND (
          JSONB_TYPEOF(document.response_document -> 'available_count') <>
            'number'
          OR document.response_document ->> 'available_count' !~
            '^-?(0|[1-9][0-9]*)$'
        ) THEN FALSE
      WHEN document.response_document ? 'available_count_known'
        AND JSONB_TYPEOF(
          document.response_document -> 'available_count_known'
        ) <> 'boolean' THEN FALSE
      WHEN document.response_document ? 'post_process_recorded'
        AND JSONB_TYPEOF(
          document.response_document -> 'post_process_recorded'
        ) <> 'boolean' THEN FALSE
      WHEN document.response_document ? 'recovery_pending'
        AND JSONB_TYPEOF(document.response_document -> 'recovery_pending') <>
          'boolean' THEN FALSE
      WHEN document.response_document ? 'recovery_deferred'
        AND JSONB_TYPEOF(document.response_document -> 'recovery_deferred') <>
          'boolean' THEN FALSE
      WHEN document.response_document ? 'account_state_recovered'
        AND JSONB_TYPEOF(
          document.response_document -> 'account_state_recovered'
        ) <> 'boolean' THEN FALSE
      WHEN document.response_document ? 'warning_code'
        AND JSONB_TYPEOF(document.response_document -> 'warning_code') <>
          'string' THEN FALSE
      ELSE TRUE
    END AS response_shape_is_valid
  FROM terminal_response_documents AS document
), terminal_response_values AS (
  SELECT
    shape.*,
    CASE
      WHEN shape.response_shape_is_valid
        THEN (shape.response_document ->> 'windows_reset')::NUMERIC
      ELSE NULL
    END AS windows_reset,
    CASE
      WHEN shape.response_shape_is_valid
        AND shape.response_document ? 'available_count'
        THEN (shape.response_document ->> 'available_count')::NUMERIC
      ELSE 0
    END AS available_count,
    COALESCE(
      shape.response_document ->> 'available_count_known' = 'true',
      FALSE
    ) AS available_count_known,
    COALESCE(
      shape.response_document ->> 'post_process_recorded' = 'true',
      FALSE
    ) AS post_process_recorded,
    COALESCE(
      shape.response_document ->> 'recovery_pending' = 'true',
      FALSE
    ) AS recovery_pending,
    COALESCE(
      shape.response_document ->> 'recovery_deferred' = 'true',
      FALSE
    ) AS recovery_deferred,
    COALESCE(
      shape.response_document ->> 'account_state_recovered' = 'true',
      FALSE
    ) AS account_state_recovered,
    COALESCE(shape.response_document ->> 'warning_code', '') AS warning_code,
    shape.response_document ? 'code' AS has_legacy_code,
    CASE shape.response_document ->> 'result_code'
      WHEN 'success' THEN 'success'
      WHEN 'no_credit' THEN 'no_credit'
      ELSE NULL
    END AS canonical_result_code,
    CASE LOWER(BTRIM(shape.response_document ->> 'code'))
      WHEN 'ok' THEN 'success'
      WHEN 'success' THEN 'success'
      WHEN 'reconciled_success' THEN 'success'
      WHEN 'no_credit' THEN 'no_credit'
      ELSE NULL
    END AS legacy_result_code
  FROM terminal_response_shapes AS shape
), terminal_response_validation AS (
  SELECT
    value.*,
    CASE
      WHEN NOT value.response_shape_is_valid THEN FALSE
      WHEN value.response_document ? 'result_code'
        AND value.canonical_result_code IS NULL THEN FALSE
      WHEN value.has_legacy_code
        AND value.legacy_result_code IS NULL THEN FALSE
      WHEN value.response_document ? 'result_code'
        AND value.has_legacy_code
        AND value.canonical_result_code IS DISTINCT FROM value.legacy_result_code
        THEN FALSE
      WHEN value.windows_reset < 0
        OR value.windows_reset > 2147483647 THEN FALSE
      WHEN value.available_count < 0
        OR value.available_count > 2147483647 THEN FALSE
      WHEN NOT value.available_count_known
        AND value.available_count <> 0 THEN FALSE
      WHEN value.recovery_deferred
        AND (
          NOT value.recovery_pending
          OR value.post_process_recorded
          OR value.account_state_recovered
        ) THEN FALSE
      WHEN value.warning_code <> ''
        AND (
          BTRIM(value.warning_code) <> value.warning_code
          OR LENGTH(value.warning_code) > 128
          OR value.warning_code !~ '^[A-Za-z0-9_.:-]+$'
          OR NOT value.recovery_pending
        ) THEN FALSE
      WHEN value.recovery_pending
        AND value.account_state_recovered
        AND value.warning_code = '' THEN FALSE
      WHEN value.post_process_recorded
        AND NOT value.account_state_recovered
        AND NOT value.recovery_pending THEN FALSE
      WHEN COALESCE(
        value.canonical_result_code,
        value.legacy_result_code
      ) = 'no_credit'
        AND (
          value.windows_reset <> 0
          OR value.available_count <> 0
          OR value.available_count_known
          OR value.post_process_recorded
          OR value.recovery_pending
          OR value.recovery_deferred
          OR value.account_state_recovered
          OR value.warning_code <> ''
        ) THEN FALSE
      WHEN COALESCE(
        value.canonical_result_code,
        value.legacy_result_code
      ) = 'success'
        AND NOT value.recovery_pending
        AND (
          (
            NOT value.has_legacy_code
            AND (
              NOT value.post_process_recorded
              OR NOT value.account_state_recovered
            )
          )
          OR value.recovery_deferred
          OR value.warning_code <> ''
        ) THEN FALSE
      ELSE TRUE
    END AS response_is_recoverable
  FROM terminal_response_values AS value
), terminal_recovery_inventory AS (
  SELECT
    validation.idempotency_record_id,
    validation.account_id,
    CASE
      WHEN NOT validation.scope_is_reachable THEN 'unreachable_scope'
      WHEN validation.fingerprint_is_reachable IS DISTINCT FROM TRUE
        THEN 'fingerprint_mismatch'
      WHEN validation.response_document ->> 'code' = '***'
        THEN 'legacy_redacted_result'
      WHEN validation.response_status IS DISTINCT FROM 200
        OR NOT validation.response_is_recoverable
        THEN 'invalid_terminal_response'
      ELSE NULL
    END AS response_state
  FROM terminal_response_validation AS validation
), terminal_final_recovery_inventory AS (
  SELECT
    inventory.idempotency_record_id,
    inventory.account_id,
    inventory.response_state
  FROM terminal_recovery_inventory AS inventory
  WHERE inventory.response_state IS NOT NULL

  UNION ALL

  SELECT
    account.idempotency_record_id,
    account.account_id,
    account.response_state
  FROM terminal_account_inventory AS account

  UNION ALL

  SELECT
    NULL::BIGINT AS idempotency_record_id,
    missing.account_id,
    'missing_attempt_record' AS response_state
  FROM terminal_missing_attempts AS missing
)
SELECT
  idempotency_record_id,
  account_id,
  response_state
FROM terminal_final_recovery_inventory
ORDER BY account_id, idempotency_record_id NULLS FIRST;

ROLLBACK;
