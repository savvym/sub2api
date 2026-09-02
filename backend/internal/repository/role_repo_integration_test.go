//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var roleIntegrationSequence uint64

const (
	roleIntegrationModeAuditPath = "/api/v1/admin/authorization/role-mode/transitions"
)

type roleIntegrationLockIdentity struct {
	pid                int
	virtualTransaction string
}

func TestUserRepositoryCreate_SeedsCompatibilityRole(t *testing.T) {
	repo := NewUserRepository(integrationEntClient, integrationDB)

	for _, legacyRole := range []string{service.RoleUser, service.RoleAdmin} {
		t.Run(legacyRole, func(t *testing.T) {
			created := createRoleIntegrationUser(t, repo, legacyRole, "compatibility")

			var authzVersion int64
			require.NoError(t, integrationDB.QueryRowContext(
				context.Background(),
				"SELECT authz_version FROM users WHERE id = $1",
				created.ID,
			).Scan(&authzVersion))
			require.Equal(t, int64(1), authzVersion)
			requireRoleIntegrationCompatibility(t, created.ID, legacyRole)
		})
	}
}

func TestUserRepositoryUpdate_ReusesOuterEntTransactionRollback(t *testing.T) {
	ctx := context.Background()
	repo := NewUserRepository(integrationEntClient, integrationDB)
	created := createRoleIntegrationUser(t, repo, service.RoleUser, "outer-tx-before")

	outerTx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = outerTx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, outerTx)

	updated := *created
	updated.Username = "outer-tx-after"
	require.NoError(t, repo.Update(txCtx, &updated, service.UserUpdateFields{Username: true}))

	inside, err := outerTx.Client().User.Get(txCtx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "outer-tx-after", inside.Username)

	require.NoError(t, outerTx.Rollback())
	var persisted string
	require.NoError(t, integrationDB.QueryRowContext(
		ctx,
		"SELECT username FROM users WHERE id = $1",
		created.ID,
	).Scan(&persisted))
	require.Equal(t, "outer-tx-before", persisted)
}

func TestRoleRepository_PromotionUpdatesCompatibilityVersionAndOutbox(t *testing.T) {
	ctx := context.Background()
	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))

	keyValue := fmt.Sprintf("sk-role-promotion-%d", nextRoleIntegrationSuffix())
	cacheKey := roleIntegrationCacheKey(keyValue)
	registerRoleIntegrationOutboxCleanup(t, cacheKey)

	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "promotion-actor")
	target := createRoleIntegrationUser(t, userRepo, service.RoleUser, "promotion-target")
	apiKey := &service.APIKey{
		UserID: target.ID,
		Key:    keyValue,
		Name:   "role-promotion",
		Status: service.StatusActive,
	}
	require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(ctx, apiKey))
	clearRoleIntegrationOutbox(t, cacheKey)

	var beforeVersion int64
	require.NoError(t, integrationDB.QueryRowContext(
		ctx,
		"SELECT authz_version FROM users WHERE id = $1",
		target.ID,
	).Scan(&beforeVersion))

	result, err := roleService.ChangeLegacyRole(ctx, service.LegacyRoleChangeInput{
		ActorUserID:        actor.ID,
		TargetUserID:       target.ID,
		ExpectedLegacyRole: service.RoleUser,
		DesiredLegacyRole:  service.RoleAdmin,
	}, nil)
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, beforeVersion+1, result.AuthzVersion)

	var persistedRole string
	var persistedVersion int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT role, authz_version
FROM users
WHERE id = $1`, target.ID).Scan(&persistedRole, &persistedVersion))
	require.Equal(t, service.RoleAdmin, persistedRole)
	require.Equal(t, beforeVersion+1, persistedVersion)
	requireRoleIntegrationCompatibility(t, target.ID, service.RoleAdmin)
	require.Equal(t, int64(1), roleIntegrationOutboxCount(t, cacheKey))
}

func TestRoleRepository_ConcurrentCrossDemotionPreservesAnActiveAdmin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	adminA := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "concurrent-admin-a")
	adminB := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "concurrent-admin-b")

	inputs := []service.LegacyRoleChangeInput{
		{
			ActorUserID:        adminA.ID,
			TargetUserID:       adminB.ID,
			ExpectedLegacyRole: service.RoleAdmin,
			DesiredLegacyRole:  service.RoleUser,
		},
		{
			ActorUserID:        adminB.ID,
			TargetUserID:       adminA.ID,
			ExpectedLegacyRole: service.RoleAdmin,
			DesiredLegacyRole:  service.RoleUser,
		},
	}

	type outcome struct {
		result service.LegacyRoleMutationResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(inputs))
	for _, input := range inputs {
		input := input
		go func() {
			<-start
			result, err := roleService.ChangeLegacyRole(ctx, input, nil)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)

	successes := 0
	failures := 0
	for range inputs {
		got := <-outcomes
		if got.err == nil {
			successes++
			require.True(t, got.result.Changed)
			continue
		}
		failures++
	}
	require.LessOrEqual(t, successes, 1, "serialized demotions must not both commit")
	require.Equal(t, 1, successes, "one of two valid cross-demotions should commit")
	require.Equal(t, 1, failures)

	var activeCandidates int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM users
WHERE id IN ($1, $2)
  AND role = 'admin'
  AND status = 'active'
  AND deleted_at IS NULL`, adminA.ID, adminB.ID).Scan(&activeCandidates))
	require.Equal(t, int64(1), activeCandidates)

	var activeAdmins int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM users
WHERE role = 'admin'
  AND status = 'active'
  AND deleted_at IS NULL`).Scan(&activeAdmins))
	require.GreaterOrEqual(t, activeAdmins, int64(1))

	requireRoleIntegrationCompatibilityMatchesLegacy(t, adminA.ID)
	requireRoleIntegrationCompatibilityMatchesLegacy(t, adminB.ID)
}

func TestRoleRepository_MixedUserMutationFailureRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))

	keyValue := fmt.Sprintf("sk-role-rollback-%d", nextRoleIntegrationSuffix())
	cacheKey := roleIntegrationCacheKey(keyValue)
	registerRoleIntegrationOutboxCleanup(t, cacheKey)

	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "rollback-actor")
	target := createRoleIntegrationUser(t, userRepo, service.RoleUser, "rollback-before")
	apiKey := &service.APIKey{
		UserID: target.ID,
		Key:    keyValue,
		Name:   "role-rollback",
		Status: service.StatusActive,
	}
	require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(ctx, apiKey))
	clearRoleIntegrationOutbox(t, cacheKey)

	var beforeVersion int64
	require.NoError(t, integrationDB.QueryRowContext(
		ctx,
		"SELECT authz_version FROM users WHERE id = $1",
		target.ID,
	).Scan(&beforeVersion))

	mutated := *target
	mutated.Username = "rollback-after"
	// This ID cannot exist in the integration database. The FK failure occurs
	// after the username UPDATE, proving both that write and the preceding role
	// reconciliation remain owned by RoleRepository's outer transaction.
	mutated.AllowedGroups = []int64{1 << 62}
	_, err := roleService.ChangeLegacyRole(ctx, service.LegacyRoleChangeInput{
		ActorUserID:        actor.ID,
		TargetUserID:       target.ID,
		ExpectedLegacyRole: service.RoleUser,
		DesiredLegacyRole:  service.RoleAdmin,
	}, func(txCtx context.Context) error {
		return userRepo.Update(txCtx, &mutated, service.UserUpdateFields{
			Username:      true,
			AllowedGroups: true,
		})
	})
	require.Error(t, err)

	var persistedRole string
	var persistedUsername string
	var persistedVersion int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT role, username, authz_version
FROM users
WHERE id = $1`, target.ID).Scan(&persistedRole, &persistedUsername, &persistedVersion))
	require.Equal(t, service.RoleUser, persistedRole)
	require.Equal(t, "rollback-before", persistedUsername)
	require.Equal(t, beforeVersion, persistedVersion)
	requireRoleIntegrationCompatibility(t, target.ID, service.RoleUser)

	var allowedGroups int64
	require.NoError(t, integrationDB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM user_allowed_groups WHERE user_id = $1",
		target.ID,
	).Scan(&allowedGroups))
	require.Zero(t, allowedGroups)
	require.Zero(t, roleIntegrationOutboxCount(t, cacheKey))
}

func TestRoleRepository_ReadinessAndModeTransitions(t *testing.T) {
	ctx := context.Background()
	forceRoleIntegrationMode(t, service.RoleAuthorizationModeLegacy)

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "transition-actor")
	registerRoleIntegrationAuditCleanup(t, actor.ID)
	subject := createRoleIntegrationUser(t, userRepo, service.RoleUser, "transition-subject")

	status, err := roleService.GetAuthorizationModeStatus(ctx, actor.ID)
	require.NoError(t, err)
	require.Equal(t, service.RoleAuthorizationModeLegacy, status.CurrentMode)
	require.Equal(t, service.RoleAuthorizationModeShadow, status.TargetMode)
	require.True(t, status.CanTransition)
	require.NotNil(t, status.Readiness.Blockers)

	toShadow, err := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeLegacy,
		TargetMode:   service.RoleAuthorizationModeShadow,
	})
	require.NoError(t, err)
	require.True(t, toShadow.Changed)
	require.True(t, toShadow.Readiness.Ready())
	require.NotNil(t, toShadow.Readiness.Blockers)
	require.Equal(t, service.RoleAuthorizationModeShadow, roleIntegrationMode(t))

	toLegacy, err := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeShadow,
		TargetMode:   service.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	require.True(t, toLegacy.Changed)
	require.Equal(t, service.RoleAuthorizationModeLegacy, roleIntegrationMode(t))

	result, err := integrationDB.ExecContext(ctx, `
DELETE FROM user_roles AS ur
USING roles AS r, service_principals AS sp
WHERE ur.user_id = $1
  AND ur.role_id = r.id
  AND ur.granted_by_service_principal_id = sp.id
  AND r.code = 'user'
  AND sp.code = 'system_bootstrap'`, subject.ID)
	require.NoError(t, err)
	deleted, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	blocked, err := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeLegacy,
		TargetMode:   service.RoleAuthorizationModeShadow,
	})
	require.ErrorIs(t, err, service.ErrRoleAuthorizationModeNotReady)
	require.False(t, blocked.Changed)
	require.Equal(t, service.RoleAuthorizationModeLegacy, roleIntegrationMode(t))
	require.GreaterOrEqual(
		t,
		roleIntegrationBlockerCount(blocked.Readiness, service.RoleReadinessCompatibilityRoleMissing),
		int64(1),
	)

	_, err = roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeLegacy,
		TargetMode:   service.RoleAuthorizationModeRBAC,
	})
	require.ErrorIs(t, err, service.ErrRBACConsumersNotMigrated)
	require.Equal(t, service.RoleAuthorizationModeLegacy, roleIntegrationMode(t))
}

func TestRoleRepository_ModeTransitionWritesDurableAudit(t *testing.T) {
	ctx := context.Background()
	forceRoleIntegrationMode(t, service.RoleAuthorizationModeLegacy)

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "transition-audit-actor")
	registerRoleIntegrationAuditCleanup(t, actor.ID)

	trace := service.RoleAuthorizationModeAuditTrace{
		RequestID: strings.Repeat("r", 80),
		ClientIP:  strings.Repeat("1", 80),
		UserAgent: strings.Repeat("u", 600),
	}
	result, err := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeLegacy,
		TargetMode:   service.RoleAuthorizationModeShadow,
		AuditTrace:   trace,
	})
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, service.RoleAuthorizationModeShadow, roleIntegrationMode(t))

	var actorUserID int64
	var actorEmail string
	var actorRole string
	var authMethod string
	var action string
	var method string
	var path string
	var requestID string
	var clientIP string
	var userAgent string
	var statusCode int
	var extra string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT actor_user_id,
       actor_email,
       actor_role,
       auth_method,
       action,
       method,
       path,
       request_id,
       client_ip,
       user_agent,
       status_code,
       extra::text
FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2
ORDER BY id DESC
LIMIT 1`, actor.ID, service.AuditActionRoleAuthorizationModeTransition).Scan(
		&actorUserID,
		&actorEmail,
		&actorRole,
		&authMethod,
		&action,
		&method,
		&path,
		&requestID,
		&clientIP,
		&userAgent,
		&statusCode,
		&extra,
	))
	require.Equal(t, actor.ID, actorUserID)
	require.Equal(t, actor.Email, actorEmail)
	require.Equal(t, service.RoleAdmin, actorRole)
	require.Equal(t, service.AuditAuthMethodJWT, authMethod)
	require.Equal(t, service.AuditActionRoleAuthorizationModeTransition, action)
	require.Equal(t, "POST", method)
	require.Equal(t, roleIntegrationModeAuditPath, path)
	require.Equal(t, strings.Repeat("r", 64), requestID)
	require.Equal(t, strings.Repeat("1", 64), clientIP)
	require.Equal(t, strings.Repeat("u", 512), userAgent)
	require.Equal(t, 200, statusCode)
	require.JSONEq(t, `{"previous_mode":"legacy","current_mode":"shadow"}`, extra)

	var auditCount int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2`, actor.ID, service.AuditActionRoleAuthorizationModeTransition).Scan(&auditCount))
	require.Equal(t, int64(1), auditCount)
}

func TestRoleRepository_ModeTransitionAuditFailureRollsBackMode(t *testing.T) {
	ctx := context.Background()
	forceRoleIntegrationMode(t, service.RoleAuthorizationModeLegacy)

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "transition-audit-failure-actor")
	registerRoleIntegrationAuditCleanup(t, actor.ID)

	suffix := nextRoleIntegrationSuffix()
	functionName := fmt.Sprintf("role_mode_audit_failure_%d", suffix)
	triggerName := fmt.Sprintf("role_mode_audit_failure_%d", suffix)
	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.action = '%s' AND NEW.request_id = 'force-role-mode-audit-failure' THEN
        RAISE EXCEPTION 'forced role mode audit failure';
    END IF;
    RETURN NEW;
END;
$$`, pq.QuoteIdentifier(functionName), service.AuditActionRoleAuthorizationModeTransition))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()",
			pq.QuoteIdentifier(functionName),
		))
		require.NoError(t, cleanupErr)
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON audit_logs
FOR EACH ROW
EXECUTE FUNCTION %s()`, pq.QuoteIdentifier(triggerName), pq.QuoteIdentifier(functionName)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON audit_logs",
			pq.QuoteIdentifier(triggerName),
		))
		require.NoError(t, cleanupErr)
	})

	result, err := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actor.ID,
		ExpectedMode: service.RoleAuthorizationModeLegacy,
		TargetMode:   service.RoleAuthorizationModeShadow,
		AuditTrace: service.RoleAuthorizationModeAuditTrace{
			RequestID: "force-role-mode-audit-failure",
		},
	})
	require.Error(t, err)
	require.False(t, result.Changed)
	require.Equal(t, service.RoleAuthorizationModeLegacy, result.PreviousMode)
	require.Equal(t, service.RoleAuthorizationModeLegacy, result.CurrentMode)
	require.Equal(t, service.RoleAuthorizationModeLegacy, roleIntegrationMode(t))

	var auditCount int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2
  AND request_id = 'force-role-mode-audit-failure'`, actor.ID, service.AuditActionRoleAuthorizationModeTransition).Scan(&auditCount))
	require.Zero(t, auditCount)
}

func TestRoleRepository_ModeStatusSnapshotDoesNotBlockUserWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	forceRoleIntegrationMode(t, service.RoleAuthorizationModeLegacy)

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "status-snapshot-actor")

	writer, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Rollback() })
	_, err = writer.ExecContext(ctx, `
UPDATE users
SET username = username
WHERE id = $1`, actor.ID)
	require.NoError(t, err)

	type statusOutcome struct {
		status service.RoleAuthorizationModeStatus
		err    error
	}
	statusDone := make(chan statusOutcome, 1)
	go func() {
		status, statusErr := roleService.GetAuthorizationModeStatus(ctx, actor.ID)
		statusDone <- statusOutcome{status: status, err: statusErr}
	}()

	select {
	case outcome := <-statusDone:
		require.NoError(t, outcome.err)
		require.Equal(t, service.RoleAuthorizationModeLegacy, outcome.status.CurrentMode)
		require.Equal(t, service.RoleAuthorizationModeShadow, outcome.status.TargetMode)
		require.True(t, outcome.status.CanTransition)
	case <-time.After(2 * time.Second):
		t.Fatal("read-only mode status snapshot blocked behind a user write")
	}
	require.NoError(t, writer.Rollback())
}

func TestRoleRepository_ModeTransitionDoesNotDeadlockWithUserUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	forceRoleIntegrationMode(t, service.RoleAuthorizationModeLegacy)

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	roleService := service.NewRoleService(NewRoleRepository(integrationEntClient, integrationDB))
	actor := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "transition-update-actor")
	registerRoleIntegrationAuditCleanup(t, actor.ID)
	newEmail := fmt.Sprintf("transition-update-%d@example.com", nextRoleIntegrationSuffix())

	// Hold the email advisory lock so UserRepository.Update pauses after taking
	// its users table and actor row locks. This deterministically exercises the
	// table-lock upgrade cycle that previously deadlocked with readiness.
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = blocker.Rollback() })
	_, err = blocker.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockHash(normalizedEmailUniquenessLockKey(newEmail)))
	require.NoError(t, err)

	updated := *actor
	updated.Email = newEmail
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- userRepo.Update(ctx, &updated, service.UserUpdateFields{Email: true})
	}()

	require.Eventually(t, func() bool {
		probe, beginErr := integrationDB.BeginTx(ctx, nil)
		if beginErr != nil {
			return false
		}
		defer func() { _ = probe.Rollback() }()
		var id int64
		scanErr := probe.QueryRowContext(ctx, `
SELECT id
FROM users
WHERE id = $1
FOR UPDATE NOWAIT`, actor.ID).Scan(&id)
		if scanErr == nil {
			return false
		}
		var pqErr *pq.Error
		return errors.As(scanErr, &pqErr) && pqErr.Code == "55P03"
	}, 3*time.Second, 10*time.Millisecond, "user update did not reach its actor row lock")

	waitingShareLocksBefore, err := roleIntegrationWaitingUsersShareLocks(ctx)
	require.NoError(t, err)

	type transitionOutcome struct {
		result service.RoleAuthorizationModeTransitionResult
		err    error
	}
	transitionDone := make(chan transitionOutcome, 1)
	go func() {
		result, transitionErr := roleService.TransitionAuthorizationMode(ctx, service.RoleAuthorizationModeTransitionInput{
			ActorUserID:  actor.ID,
			ExpectedMode: service.RoleAuthorizationModeLegacy,
			TargetMode:   service.RoleAuthorizationModeShadow,
		})
		transitionDone <- transitionOutcome{result: result, err: transitionErr}
	}()

	require.Eventually(t, func() bool {
		waitingShareLocks, queryErr := roleIntegrationWaitingUsersShareLocks(ctx)
		if queryErr != nil {
			return false
		}
		for identity := range waitingShareLocks {
			if _, existed := waitingShareLocksBefore[identity]; !existed {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond, "mode transition did not wait for ShareLock on users")

	// Releasing this lock lets the user update finish. Both operations must then
	// commit; an inconsistent row-before-table order makes PostgreSQL abort one
	// side of the cycle as a deadlock victim.
	require.NoError(t, blocker.Commit())
	require.NoError(t, <-updateDone)
	transition := <-transitionDone
	require.NoError(t, transition.err)
	require.True(t, transition.result.Changed)
	require.Equal(t, service.RoleAuthorizationModeShadow, roleIntegrationMode(t))

	persisted, err := userRepo.GetByID(ctx, actor.ID)
	require.NoError(t, err)
	require.Equal(t, newEmail, persisted.Email)
}

func roleIntegrationWaitingUsersShareLocks(ctx context.Context) (map[roleIntegrationLockIdentity]struct{}, error) {
	rows, err := integrationDB.QueryContext(ctx, `
SELECT pid, virtualtransaction
FROM pg_locks
WHERE locktype = 'relation'
  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
  AND relation = 'users'::regclass
  AND mode = 'ShareLock'
  AND NOT granted
  AND pid IS NOT NULL
  AND virtualtransaction IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	locks := make(map[roleIntegrationLockIdentity]struct{})
	for rows.Next() {
		var identity roleIntegrationLockIdentity
		if err := rows.Scan(&identity.pid, &identity.virtualTransaction); err != nil {
			return nil, err
		}
		locks[identity] = struct{}{}
	}
	return locks, rows.Err()
}

func createRoleIntegrationUser(
	t *testing.T,
	repo service.UserRepository,
	legacyRole string,
	username string,
) *service.User {
	t.Helper()
	suffix := nextRoleIntegrationSuffix()
	created := &service.User{
		Email:        fmt.Sprintf("role-integration-%d@example.com", suffix),
		Username:     username,
		PasswordHash: "role-integration-password-hash",
		Role:         legacyRole,
		Status:       service.StatusActive,
		Concurrency:  5,
	}
	require.NoError(t, repo.Create(context.Background(), created))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(
			context.Background(),
			"DELETE FROM users WHERE id = $1",
			created.ID,
		)
		require.NoError(t, err, "hard-delete role integration user %d", created.ID)
	})
	return created
}

func nextRoleIntegrationSuffix() int64 {
	sequence := atomic.AddUint64(&roleIntegrationSequence, 1)
	return time.Now().UnixNano() + int64(sequence)
}

func requireRoleIntegrationCompatibility(t *testing.T, userID int64, wantRole string) {
	t.Helper()
	type assignment struct {
		role      string
		grantor   string
		permanent bool
	}
	rows, err := integrationDB.QueryContext(context.Background(), `
SELECT r.code, COALESCE(sp.code, ''), ur.expires_at IS NULL
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
LEFT JOIN service_principals AS sp ON sp.id = ur.granted_by_service_principal_id
WHERE ur.user_id = $1
  AND r.code IN ('admin', 'user')
ORDER BY r.code`, userID)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	assignments := make([]assignment, 0, 2)
	for rows.Next() {
		var got assignment
		require.NoError(t, rows.Scan(&got.role, &got.grantor, &got.permanent))
		assignments = append(assignments, got)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []assignment{{
		role:      wantRole,
		grantor:   "system_bootstrap",
		permanent: true,
	}}, assignments)
}

func requireRoleIntegrationCompatibilityMatchesLegacy(t *testing.T, userID int64) {
	t.Helper()
	var legacyRole string
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT role FROM users WHERE id = $1",
		userID,
	).Scan(&legacyRole))
	requireRoleIntegrationCompatibility(t, userID, legacyRole)
}

func roleIntegrationCacheKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func registerRoleIntegrationOutboxCleanup(t *testing.T, cacheKey string) {
	t.Helper()
	// Register before user cleanup. Cleanups run LIFO, so users/API keys are
	// hard-deleted first and any delete-trigger event is removed afterwards.
	t.Cleanup(func() { clearRoleIntegrationOutbox(t, cacheKey) })
}

func clearRoleIntegrationOutbox(t *testing.T, cacheKey string) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `
DELETE FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, cacheKey)
	require.NoError(t, err)
}

func roleIntegrationOutboxCount(t *testing.T, cacheKey string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, cacheKey).Scan(&count))
	return count
}

func registerRoleIntegrationAuditCleanup(t *testing.T, actorUserID int64) {
	t.Helper()
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `
DELETE FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2`, actorUserID, service.AuditActionRoleAuthorizationModeTransition)
		require.NoError(t, err)
	})
}

func forceRoleIntegrationMode(t *testing.T, mode string) {
	t.Helper()
	ctx := context.Background()
	var previous string
	err := integrationDB.QueryRowContext(ctx, `
SELECT value
FROM settings
WHERE key = $1`, service.SettingKeyRoleAuthorizationMode).Scan(&previous)
	hadPrevious := err == nil
	require.True(t, hadPrevious || err == sql.ErrNoRows, "read prior role mode: %v", err)

	t.Cleanup(func() {
		if !hadPrevious {
			_, cleanupErr := integrationDB.ExecContext(context.Background(), `
DELETE FROM settings
WHERE key = $1`, service.SettingKeyRoleAuthorizationMode)
			require.NoError(t, cleanupErr)
			return
		}
		_, cleanupErr := integrationDB.ExecContext(context.Background(), `
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at`, service.SettingKeyRoleAuthorizationMode, previous)
		require.NoError(t, cleanupErr)
	})

	_, err = integrationDB.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at`, service.SettingKeyRoleAuthorizationMode, mode)
	require.NoError(t, err)
}

func roleIntegrationMode(t *testing.T) string {
	t.Helper()
	var mode string
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT value
FROM settings
WHERE key = $1`, service.SettingKeyRoleAuthorizationMode).Scan(&mode))
	return mode
}

func roleIntegrationBlockerCount(readiness service.RoleAuthorizationReadiness, code string) int64 {
	for _, blocker := range readiness.Blockers {
		if blocker.Code == code {
			return blocker.Count
		}
	}
	return 0
}
