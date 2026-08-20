-- Read-only Phase 0 preflight. Run against a read replica or a transaction
-- configured as READ ONLY. This script emits counts and identifiers only; it
-- never returns account credentials, API keys, or other secret values.

BEGIN TRANSACTION READ ONLY;

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

ROLLBACK;
