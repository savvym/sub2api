-- Repair compatibility roles for users created after migration 229, including
-- the initial administrator created by the legacy setup flow. Preserve the
-- exact convergence and provenance semantics established by migration 229:
-- only system_bootstrap-owned user/admin grants may be replaced.

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
