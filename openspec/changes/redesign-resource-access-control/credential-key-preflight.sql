-- Read-only production credential-key inventory. Run only against an approved
-- read replica or with a role that is independently enforced as read-only.
--
-- The result contains document names, deletion state, account status,
-- platform/type, JSON key names, JSON shapes, and aggregate counts. It never selects JSON
-- values or account identifiers. Key names may still be user-controlled, so
-- treat the output as a restricted security-review artifact.

BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '60s';
SET LOCAL idle_in_transaction_session_timeout = '2min';

WITH document_keys AS (
  SELECT
    document.document_name,
    CASE WHEN a.deleted_at IS NULL THEN 'present' ELSE 'deleted' END AS deletion_state,
    a.status AS account_status,
    a.platform,
    a.type AS auth_type,
    key_name
  FROM accounts AS a
  CROSS JOIN LATERAL (
    VALUES
      ('credentials'::text, a.credentials),
      ('extra'::text, a.extra)
  ) AS document(document_name, document_value)
  CROSS JOIN LATERAL jsonb_object_keys(
    CASE
      WHEN jsonb_typeof(document.document_value) = 'object' THEN document.document_value
      ELSE '{}'::jsonb
    END
  ) AS document_key(key_name)
)
SELECT
  document_name,
  deletion_state,
  account_status,
  platform,
  auth_type,
  key_name,
  COUNT(*) AS account_count
FROM document_keys
GROUP BY document_name, deletion_state, account_status, platform, auth_type, key_name
ORDER BY document_name, deletion_state, account_status, platform, auth_type, key_name;

WITH document_shapes AS (
  SELECT
    document.document_name,
    CASE WHEN a.deleted_at IS NULL THEN 'present' ELSE 'deleted' END AS deletion_state,
    a.status AS account_status,
    a.platform,
    a.type AS auth_type,
    COALESCE(jsonb_typeof(document.document_value), 'sql_null') AS json_shape
  FROM accounts AS a
  CROSS JOIN LATERAL (
    VALUES
      ('credentials'::text, a.credentials),
      ('extra'::text, a.extra)
  ) AS document(document_name, document_value)
)
SELECT
  document_name,
  deletion_state,
  account_status,
  platform,
  auth_type,
  json_shape,
  COUNT(*) AS account_count
FROM document_shapes
GROUP BY document_name, deletion_state, account_status, platform, auth_type, json_shape
ORDER BY document_name, deletion_state, account_status, platform, auth_type, json_shape;

ROLLBACK;
