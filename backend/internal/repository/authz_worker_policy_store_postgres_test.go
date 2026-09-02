package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// Run with a PostgreSQL administrator URL whose role may create databases:
//
//	SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN='postgres://user@127.0.0.1:5432/postgres?sslmode=disable' \
//	  go test ./internal/repository -run '^TestAuthzWorkerPolicyStorePostgresMigration243AuthorizationMatrix$' -count=1 -v
func TestAuthzWorkerPolicyStorePostgresMigration243AuthorizationMatrix(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to temporary worker-policy database: %v", err)
	}

	t.Run("roleless seeded principal authorizes capability and existing account", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)

		var roleCount, directPermissionCount, expectedPermissionCount int
		if err := tx.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM service_principal_roles WHERE service_principal_id = $1),
    (SELECT COUNT(*) FROM service_principal_worker_permissions WHERE service_principal_id = $1),
    (
        SELECT COUNT(*)
        FROM service_principal_worker_permissions AS worker_grant
        JOIN permissions AS permission ON permission.id = worker_grant.permission_id
        WHERE worker_grant.service_principal_id = $1
          AND permission.code = $2
    )
`, principalID, authz.CapabilityPlatformAccountOpenAIQuotaAutoReset).Scan(
			&roleCount,
			&directPermissionCount,
			&expectedPermissionCount,
		); err != nil {
			t.Fatalf("inspect migration 243 worker seed: %v", err)
		}
		if roleCount != 0 || directPermissionCount != 1 || expectedPermissionCount != 1 {
			t.Fatalf(
				"migration 243 worker shape = roles:%d direct:%d expected:%d, want 0/1/1",
				roleCount,
				directPermissionCount,
				expectedPermissionCount,
			)
		}

		if err := policy.CheckWorkerCapability(
			ctx,
			actor,
			authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
		); err != nil {
			t.Fatalf("authorize seeded worker capability: %v", err)
		}

		ownerID := insertAuthzPolicyPostgresUser(t, ctx, tx, "worker-policy-owner@example.test")
		accountID := insertAuthzPolicyPostgresAccount(
			t,
			ctx,
			tx,
			"worker-policy-existing-account",
			ownerID,
			"",
			false,
		)
		accountRef := mustAuthzWorkerPolicyPostgresAccountRef(t, accountID)
		if err := policy.AuthorizeWorker(
			ctx,
			actor,
			authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			authz.ActionAccountOperate,
			accountRef,
		); err != nil {
			t.Fatalf("authorize seeded worker for existing account: %v", err)
		}
	})

	t.Run("roleful principal is rejected by both database and actor snapshots", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		rolelessActor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		baselineVersion := loadAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID)

		grantorID := insertAuthzPolicyPostgresUser(t, ctx, tx, "worker-policy-role-grantor@example.test")
		roleID := insertAuthzPolicyPostgresRole(
			t,
			ctx,
			tx,
			"worker_policy_forbidden_role",
			authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
		)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id)
VALUES ($1, $2, $3)
`, principalID, roleID, grantorID); err != nil {
			t.Fatalf("attach forbidden worker role: %v", err)
		}
		setAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID, baselineVersion)

		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				rolelessActor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrPolicyAccessDenied,
			"database role count",
		)

		rolefulActor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				rolefulActor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"roleful actor snapshot",
		)
	})

	t.Run("missing direct permission is rejected and invalidates a resolved actor", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		baselineVersion := loadAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID)

		result, err := tx.ExecContext(ctx, `
DELETE FROM service_principal_worker_permissions
WHERE service_principal_id = $1
`, principalID)
		if err != nil {
			t.Fatalf("remove worker direct permission: %v", err)
		}
		assertAuthzWorkerPolicyPostgresRowsAffected(t, result, 1, "remove worker direct permission")
		assertAuthzWorkerPolicyPostgresVersionAdvanced(t, ctx, tx, principalID, baselineVersion)

		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"actor stale after direct-permission deletion",
		)

		setAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID, baselineVersion)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrPolicyAccessDenied,
			"database missing direct permission",
		)

		permissionlessActor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				permissionlessActor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"permissionless actor snapshot",
		)
	})

	t.Run("extra direct permission is rejected and invalidates a resolved actor", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		baselineVersion := loadAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID)

		result, err := tx.ExecContext(ctx, `
INSERT INTO service_principal_worker_permissions (service_principal_id, permission_id)
SELECT $1, id
FROM permissions
WHERE code = $2
`, principalID, authz.CapabilityPlatformResourceViewAll)
		if err != nil {
			t.Fatalf("attach extra worker direct permission: %v", err)
		}
		assertAuthzWorkerPolicyPostgresRowsAffected(t, result, 1, "attach extra worker direct permission")
		assertAuthzWorkerPolicyPostgresVersionAdvanced(t, ctx, tx, principalID, baselineVersion)

		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"actor stale after extra direct permission",
		)

		setAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID, baselineVersion)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrPolicyAccessDenied,
			"database extra direct permission",
		)

		overprivilegedActor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				overprivilegedActor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"overprivileged actor snapshot",
		)
	})

	t.Run("disabled principal is rejected by resolver and worker policy", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)

		if _, err := tx.ExecContext(ctx, `
UPDATE service_principals
SET status = 'disabled'
WHERE id = $1
`, principalID); err != nil {
			t.Fatalf("disable worker service principal: %v", err)
		}
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrActorInactive,
			"disabled database principal",
		)

		_, err := resolver.ResolveServicePrincipal(
			ctx,
			authz.OpenAIQuotaAutoResetServicePrincipalCode,
			authz.AuthMethodServicePrincipal,
		)
		assertAuthzWorkerPolicyPostgresErrorIs(t, err, authz.ErrActorInactive, "disabled principal resolution")
	})

	t.Run("stale principal actor is rejected while a current actor succeeds", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		staleActor, principalID := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)

		if _, err := tx.ExecContext(ctx, `
UPDATE service_principals
SET authz_version = authz_version + 1
WHERE id = $1
`, principalID); err != nil {
			t.Fatalf("advance worker authorization version: %v", err)
		}
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.CheckWorkerCapability(
				ctx,
				staleActor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
			),
			authz.ErrSessionInvalid,
			"stale worker actor",
		)

		currentActor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)
		if err := policy.CheckWorkerCapability(
			ctx,
			currentActor,
			authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
		); err != nil {
			t.Fatalf("authorize current worker actor after version advance: %v", err)
		}
	})

	t.Run("missing account is rejected", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)

		var missingAccountID int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) + 1000 FROM accounts`).Scan(&missingAccountID); err != nil {
			t.Fatalf("choose missing account ID: %v", err)
		}
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.AuthorizeWorker(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
				authz.ActionAccountOperate,
				mustAuthzWorkerPolicyPostgresAccountRef(t, missingAccountID),
			),
			authz.ErrPolicyAccessDenied,
			"missing account",
		)
	})

	t.Run("soft-deleted account is rejected", func(t *testing.T) {
		tx := beginAuthzWorkerPolicyPostgresTransaction(t, ctx, db)
		resolver, policy := newAuthzWorkerPolicyPostgresRuntime(tx)
		actor, _ := resolveOpenAIQuotaAutoResetPostgresActor(t, ctx, resolver)

		ownerID := insertAuthzPolicyPostgresUser(t, ctx, tx, "worker-policy-deleted-owner@example.test")
		accountID := insertAuthzPolicyPostgresAccount(
			t,
			ctx,
			tx,
			"worker-policy-deleted-account",
			ownerID,
			"",
			true,
		)
		assertAuthzWorkerPolicyPostgresErrorIs(
			t,
			policy.AuthorizeWorker(
				ctx,
				actor,
				authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
				authz.ActionAccountOperate,
				mustAuthzWorkerPolicyPostgresAccountRef(t, accountID),
			),
			authz.ErrPolicyAccessDenied,
			"soft-deleted account",
		)
	})
}

func beginAuthzWorkerPolicyPostgresTransaction(t *testing.T, ctx context.Context, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin worker-policy fixture transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func newAuthzWorkerPolicyPostgresRuntime(tx *sql.Tx) (authz.Resolver, authz.WorkerPolicy) {
	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx})
	client := dbent.NewClient(dbent.Driver(driver))
	return authz.NewActorResolver(NewAuthzActorResolverStore(client)),
		authz.NewWorkerPolicy(NewAuthzWorkerPolicyStore(client))
}

func resolveOpenAIQuotaAutoResetPostgresActor(
	t *testing.T,
	ctx context.Context,
	resolver authz.Resolver,
) (authz.Actor, int64) {
	t.Helper()
	actor, err := resolver.ResolveServicePrincipal(
		ctx,
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		authz.AuthMethodServicePrincipal,
	)
	if err != nil {
		t.Fatalf("resolve OpenAI quota auto-reset worker: %v", err)
	}
	principalID, ok := actor.ServicePrincipalID()
	if !ok || principalID <= 0 {
		t.Fatalf("resolved worker has invalid service principal identity: actor=%+v", actor)
	}
	return actor, principalID
}

func loadAuthzWorkerPolicyPostgresPrincipalVersion(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	principalID int64,
) int64 {
	t.Helper()
	var version int64
	if err := tx.QueryRowContext(ctx, `
SELECT authz_version
FROM service_principals
WHERE id = $1
`, principalID).Scan(&version); err != nil {
		t.Fatalf("load worker authorization version: %v", err)
	}
	return version
}

func setAuthzWorkerPolicyPostgresPrincipalVersion(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	principalID int64,
	version int64,
) {
	t.Helper()
	// The rollback-only fixture has already proved that the grant trigger
	// advanced this version. Pinning it here bypasses the stale-actor check so
	// the same actor can exercise the store's independent role/grant checks.
	if _, err := tx.ExecContext(ctx, `
UPDATE service_principals
SET authz_version = $2
WHERE id = $1
`, principalID, version); err != nil {
		t.Fatalf("set worker authorization version: %v", err)
	}
}

func assertAuthzWorkerPolicyPostgresVersionAdvanced(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	principalID int64,
	previous int64,
) {
	t.Helper()
	current := loadAuthzWorkerPolicyPostgresPrincipalVersion(t, ctx, tx, principalID)
	if current <= previous {
		t.Fatalf("worker authorization version = %d, want greater than %d", current, previous)
	}
}

func mustAuthzWorkerPolicyPostgresAccountRef(t *testing.T, accountID int64) authz.ResourceRef {
	t.Helper()
	ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, accountID)
	if err != nil {
		t.Fatalf("create worker-policy Account reference %d: %v", accountID, err)
	}
	return ref
}

func assertAuthzWorkerPolicyPostgresRowsAffected(
	t *testing.T,
	result sql.Result,
	want int64,
	operation string,
) {
	t.Helper()
	got, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("%s rows affected: %v", operation, err)
	}
	if got != want {
		t.Fatalf("%s affected %d rows, want %d", operation, got, want)
	}
}

func assertAuthzWorkerPolicyPostgresErrorIs(t *testing.T, got, want error, operation string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s error = %v, want %v", operation, got, want)
	}
}
