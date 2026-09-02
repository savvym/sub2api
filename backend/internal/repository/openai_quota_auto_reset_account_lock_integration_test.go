//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaAutoResetAccountLockerIntegrationContention(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	firstAccount := newOpenAIAutoResetAccountLockIntegrationAccount(t, "contention-first")
	secondAccount := newOpenAIAutoResetAccountLockIntegrationAccount(t, "contention-second")
	secondDB := openOpenAIAutoResetAccountLockIntegrationDB(t)

	firstLocker := NewOpenAIQuotaAutoResetAccountLocker(integrationDB)
	secondLocker := NewOpenAIQuotaAutoResetAccountLocker(secondDB)

	requestCtx, cancelRequest := context.WithCancel(ctx)
	firstLease, acquired, err := firstLocker.TryAcquire(requestCtx, firstAccount.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, firstLease)
	firstReleased := false
	t.Cleanup(func() {
		if !firstReleased {
			require.NoError(t, firstLease.Release())
		}
	})

	// Cancelling the request that acquired the transaction must not release it.
	cancelRequest()
	contendedLease, acquired, err := secondLocker.TryAcquire(ctx, firstAccount.ID)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, contendedLease)

	otherLease, acquired, err := secondLocker.TryAcquire(ctx, secondAccount.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, otherLease)
	require.NoError(t, otherLease.Release())

	require.NoError(t, firstLease.Release())
	firstReleased = true

	reacquiredLease, acquired, err := secondLocker.TryAcquire(ctx, firstAccount.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, reacquiredLease)
	require.NoError(t, reacquiredLease.Release())
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationBlocksEligibilityWritersUntilRelease(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		assert func(*testing.T, context.Context, int64)
	}{
		{
			name: "configuration disable",
			query: `
UPDATE accounts
SET extra = jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{auto_reset_credit_enabled}',
        'false'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE id = $1`,
			assert: func(t *testing.T, ctx context.Context, accountID int64) {
				var enabled bool
				require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COALESCE((extra ->> 'auto_reset_credit_enabled')::boolean, false)
FROM accounts
WHERE id = $1`, accountID).Scan(&enabled))
				require.False(t, enabled)
			},
		},
		{
			name:  "status disable",
			query: `UPDATE accounts SET status = 'disabled', updated_at = NOW() WHERE id = $1`,
			assert: func(t *testing.T, ctx context.Context, accountID int64) {
				var status string
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = $1`, accountID).Scan(&status))
				require.Equal(t, service.StatusDisabled, status)
			},
		},
		{
			name:  "schedulable disable",
			query: `UPDATE accounts SET schedulable = FALSE, updated_at = NOW() WHERE id = $1`,
			assert: func(t *testing.T, ctx context.Context, accountID int64) {
				var schedulable bool
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT schedulable FROM accounts WHERE id = $1`, accountID).Scan(&schedulable))
				require.False(t, schedulable)
			},
		},
		{
			name:  "soft delete",
			query: `UPDATE accounts SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1`,
			assert: func(t *testing.T, ctx context.Context, accountID int64) {
				var deletedAt sql.NullTime
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT deleted_at FROM accounts WHERE id = $1`, accountID).Scan(&deletedAt))
				require.True(t, deletedAt.Valid)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			account := newOpenAIAutoResetAccountLockIntegrationAccount(t, tt.name)
			lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(integrationDB).TryAcquire(ctx, account.ID)
			require.NoError(t, err)
			require.True(t, acquired)
			require.NotNil(t, lease)
			leaseReleased := false
			t.Cleanup(func() {
				if !leaseReleased {
					require.NoError(t, lease.Release())
				}
			})
			exists, err := lease.LockAccountRow(ctx)
			require.NoError(t, err)
			require.True(t, exists)

			writerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
			writerConn, err := writerDB.Conn(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, writerConn.Close()) })
			var writerPID int
			require.NoError(t, writerConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID))

			var externalEffectFinished atomic.Bool
			writerDone := make(chan error, 1)
			go func() {
				_, writeErr := writerConn.ExecContext(ctx, tt.query, account.ID)
				if writeErr == nil && !externalEffectFinished.Load() {
					writeErr = errors.New("eligibility writer committed before the simulated external effect finished")
				}
				writerDone <- writeErr
			}()

			requirePostgresBackendWaitingOnLock(t, ctx, writerPID)
			select {
			case writeErr := <-writerDone:
				require.Failf(t, "writer completed while lease was held", "error: %v", writeErr)
			default:
			}

			// The reset side effect linearizes while both the advisory and account
			// row locks are held. Only after it completes may a disabling writer win.
			externalEffectFinished.Store(true)
			require.NoError(t, lease.Release())
			leaseReleased = true
			require.NoError(t, <-writerDone)
			tt.assert(t, ctx, account.ID)
		})
	}
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationAdvisoryPhaseDoesNotBlockAccountOrCredentialWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	account := newOpenAIAutoResetAccountLockIntegrationAccount(t, "advisory-only-writes")
	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(integrationDB).TryAcquire(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			require.NoError(t, lease.Release())
		}
	})

	writerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	writeCtx, cancelWrite := context.WithTimeout(ctx, 2*time.Second)
	_, err = writerDB.ExecContext(writeCtx, `
UPDATE accounts
SET extra = jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{codex_auto_reset_credit_state}',
        '{"status":"resetting"}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE id = $1`, account.ID)
	cancelWrite()
	require.NoError(t, err, "the advisory-only phase must not self-deadlock account state persistence")

	writeCtx, cancelWrite = context.WithTimeout(ctx, 2*time.Second)
	_, err = writerDB.ExecContext(writeCtx, `
UPDATE accounts
SET credentials = '{"access_token":"rotated-during-advisory-phase"}'::jsonb,
    updated_at = NOW()
WHERE id = $1`, account.ID)
	cancelWrite()
	require.NoError(t, err, "the advisory-only phase must not block credential rotation")

	var accessToken string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT credentials ->> 'access_token'
FROM accounts
WHERE id = $1`, account.ID).Scan(&accessToken))
	require.Equal(t, "rotated-during-advisory-phase", accessToken)

	exists, err := lease.LockAccountRow(ctx)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, lease.Release())
	leaseReleased = true
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationUpgradedLeaseBlocksBoundProxyAndAccountWritersUntilRelease(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		targetID func(*service.Account, *service.Proxy) int64
		assert   func(*testing.T, context.Context, *service.Account, *service.Proxy)
	}{
		{
			name:  "proxy content update",
			query: `UPDATE proxies SET host = '127.0.0.2', updated_at = NOW() WHERE id = $1`,
			targetID: func(_ *service.Account, proxy *service.Proxy) int64 {
				return proxy.ID
			},
			assert: func(t *testing.T, ctx context.Context, _ *service.Account, proxy *service.Proxy) {
				var host string
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT host FROM proxies WHERE id = $1`, proxy.ID).Scan(&host))
				require.Equal(t, "127.0.0.2", host)
			},
		},
		{
			name:  "proxy delete",
			query: `DELETE FROM proxies WHERE id = $1`,
			targetID: func(_ *service.Account, proxy *service.Proxy) int64 {
				return proxy.ID
			},
			assert: func(t *testing.T, ctx context.Context, account *service.Account, proxy *service.Proxy) {
				var proxyCount int
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxies WHERE id = $1`, proxy.ID).Scan(&proxyCount))
				require.Zero(t, proxyCount)

				var accountProxyID sql.NullInt64
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT proxy_id FROM accounts WHERE id = $1`, account.ID).Scan(&accountProxyID))
				require.False(t, accountProxyID.Valid, "the proxy FK must be cleared after deletion wins")
			},
		},
		{
			name:  "account credential update",
			query: `UPDATE accounts SET credentials = '{"access_token":"rotated-after-reset"}'::jsonb, updated_at = NOW() WHERE id = $1`,
			targetID: func(account *service.Account, _ *service.Proxy) int64 {
				return account.ID
			},
			assert: func(t *testing.T, ctx context.Context, account *service.Account, _ *service.Proxy) {
				var accessToken string
				require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT credentials ->> 'access_token'
FROM accounts
WHERE id = $1`, account.ID).Scan(&accessToken))
				require.Equal(t, "rotated-after-reset", accessToken)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			account, proxy := newOpenAIAutoResetAccountLockIntegrationProxiedAccount(t, tt.name)
			writerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
			writerConn, err := writerDB.Conn(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, writerConn.Close()) })
			var writerPID int
			require.NoError(t, writerConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID))

			lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(integrationDB).TryAcquire(ctx, account.ID)
			require.NoError(t, err)
			require.True(t, acquired)
			require.NotNil(t, lease)
			leaseReleased := false
			t.Cleanup(func() {
				if !leaseReleased {
					require.NoError(t, lease.Release())
				}
			})
			exists, err := lease.LockAccountRow(ctx)
			require.NoError(t, err)
			require.True(t, exists)

			var externalEffectFinished atomic.Bool
			writerDone := make(chan error, 1)
			go func() {
				_, writeErr := writerConn.ExecContext(ctx, tt.query, tt.targetID(account, proxy))
				if writeErr == nil && !externalEffectFinished.Load() {
					writeErr = errors.New("bound proxy/account writer committed before the simulated external effect finished")
				}
				writerDone <- writeErr
			}()

			requirePostgresBackendWaitingOnLock(t, ctx, writerPID)
			select {
			case writeErr := <-writerDone:
				require.Failf(t, "writer completed while upgraded lease was held", "error: %v", writeErr)
			default:
			}

			externalEffectFinished.Store(true)
			require.NoError(t, lease.Release())
			leaseReleased = true
			require.NoError(t, <-writerDone)
			tt.assert(t, ctx, account, proxy)
		})
	}
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationProxyExpiryAndLeaseUseProxyThenAccountOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	account, proxy := newOpenAIAutoResetAccountLockIntegrationProxiedAccount(t, "proxy-expiry-lock-order")

	lockerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	lockerDB.SetMaxOpenConns(1)
	lockerDB.SetMaxIdleConns(1)
	var lockerPID int
	require.NoError(t, lockerDB.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&lockerPID))

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(lockerDB).TryAcquire(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			require.NoError(t, lease.Release())
		}
	})

	expiryDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	expiryConn, err := expiryDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, expiryConn.Close()) })
	expiryTx, err := expiryConn.BeginTx(ctx, nil)
	require.NoError(t, err)
	expiryFinished := false
	t.Cleanup(func() {
		if !expiryFinished {
			_ = expiryTx.Rollback()
		}
	})

	// Mirror sweepOneExpiredProxyOnExec: the expiry path locks the proxy first,
	// then rewrites the accounts bound to it in the same transaction.
	_, err = expiryTx.ExecContext(ctx, `
UPDATE proxies
SET status = $1, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL`, service.StatusExpired, proxy.ID)
	require.NoError(t, err)

	type protectResult struct {
		exists bool
		err    error
	}
	protectDone := make(chan protectResult, 1)
	go func() {
		exists, protectErr := lease.LockAccountRow(ctx)
		protectDone <- protectResult{exists: exists, err: protectErr}
	}()

	// The lease must wait on the proxy instead of taking the account row first.
	// Otherwise this account rewrite would form proxy->account/account->proxy.
	requirePostgresBackendWaitingOnLock(t, ctx, lockerPID)
	accountWriteCtx, cancelAccountWrite := context.WithTimeout(ctx, 5*time.Second)
	_, err = expiryTx.ExecContext(accountWriteCtx, `
UPDATE accounts
SET proxy_id = NULL,
    proxy_fallback_origin_id = $1,
    updated_at = NOW()
WHERE id = $2`, proxy.ID, account.ID)
	cancelAccountWrite()
	require.NoError(t, err, "the proxy-first expiry transaction must not deadlock with the lease upgrade")
	require.NoError(t, expiryTx.Commit())
	expiryFinished = true

	result := <-protectDone
	require.NoError(t, result.err)
	require.False(t, result.exists, "a proxy switch that wins before the account lock must fail the upgrade closed")
	require.NoError(t, lease.Release())
	leaseReleased = true

	var proxyStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM proxies WHERE id = $1`, proxy.ID).Scan(&proxyStatus))
	require.Equal(t, service.StatusExpired, proxyStatus)
	var accountProxyID sql.NullInt64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT proxy_id FROM accounts WHERE id = $1`, account.ID).Scan(&accountProxyID))
	require.False(t, accountProxyID.Valid)
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationUpgradedLeaseBeforeProxyExpiryDoesNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	account, proxy := newOpenAIAutoResetAccountLockIntegrationProxiedAccount(t, "lease-before-proxy-expiry")
	expiryDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	expiryConn, err := expiryDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, expiryConn.Close()) })
	var expiryPID int
	require.NoError(t, expiryConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&expiryPID))
	expiryTx, err := expiryConn.BeginTx(ctx, nil)
	require.NoError(t, err)
	expiryFinished := false
	t.Cleanup(func() {
		if !expiryFinished {
			_ = expiryTx.Rollback()
		}
	})

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(integrationDB).TryAcquire(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			require.NoError(t, lease.Release())
		}
	})
	exists, err := lease.LockAccountRow(ctx)
	require.NoError(t, err)
	require.True(t, exists)

	expiryDone := make(chan error, 1)
	go func() {
		_, expiryErr := expiryTx.ExecContext(ctx, `
UPDATE proxies
SET status = $1, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL`, service.StatusExpired, proxy.ID)
		if expiryErr == nil {
			_, expiryErr = expiryTx.ExecContext(ctx, `
UPDATE accounts
SET proxy_id = NULL,
    proxy_fallback_origin_id = $1,
    updated_at = NOW()
WHERE id = $2`, proxy.ID, account.ID)
		}
		if expiryErr == nil {
			expiryErr = expiryTx.Commit()
		}
		expiryDone <- expiryErr
	}()

	requirePostgresBackendWaitingOnLock(t, ctx, expiryPID)
	select {
	case expiryErr := <-expiryDone:
		require.Failf(t, "proxy expiry completed while upgraded lease was held", "error: %v", expiryErr)
	default:
	}

	require.NoError(t, lease.Release())
	leaseReleased = true
	require.NoError(t, <-expiryDone, "proxy-first expiry must finish after the lease releases both rows")
	expiryFinished = true

	var proxyStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM proxies WHERE id = $1`, proxy.ID).Scan(&proxyStatus))
	require.Equal(t, service.StatusExpired, proxyStatus)
	var accountProxyID sql.NullInt64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT proxy_id FROM accounts WHERE id = $1`, account.ID).Scan(&accountProxyID))
	require.False(t, accountProxyID.Valid)
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationDeleteWinsBeforeRowLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	account := newOpenAIAutoResetAccountLockIntegrationAccount(t, "delete-first")
	writerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	writerTx, err := writerDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	writerFinished := false
	t.Cleanup(func() {
		if !writerFinished {
			_ = writerTx.Rollback()
		}
	})
	_, err = writerTx.ExecContext(ctx, `UPDATE accounts SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, account.ID)
	require.NoError(t, err)

	lockerDB := openOpenAIAutoResetAccountLockIntegrationDB(t)
	lockerDB.SetMaxOpenConns(1)
	lockerDB.SetMaxIdleConns(1)
	var lockerPID int
	require.NoError(t, lockerDB.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&lockerPID))

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(lockerDB).TryAcquire(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			require.NoError(t, lease.Release())
		}
	})

	type protectResult struct {
		exists bool
		err    error
	}
	protectDone := make(chan protectResult, 1)
	go func() {
		exists, protectErr := lease.LockAccountRow(ctx)
		protectDone <- protectResult{exists: exists, err: protectErr}
	}()

	requirePostgresBackendWaitingOnLock(t, ctx, lockerPID)
	require.NoError(t, writerTx.Commit())
	writerFinished = true

	result := <-protectDone
	require.NoError(t, result.err)
	require.False(t, result.exists, "a concurrently deleted account must fail closed at the row-lock upgrade")
	require.NoError(t, lease.Release())
	leaseReleased = true
}

func TestOpenAIQuotaAutoResetAccountLockerIntegrationMissingAccountFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var missingAccountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) + 1000000 FROM accounts`).Scan(&missingAccountID))
	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(integrationDB).TryAcquire(ctx, missingAccountID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	exists, err := lease.LockAccountRow(ctx)
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, lease.Release())
}

func requirePostgresBackendWaitingOnLock(t *testing.T, ctx context.Context, backendPID int) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waitEventType sql.NullString
		err := integrationDB.QueryRowContext(ctx, `
SELECT wait_event_type
FROM pg_stat_activity
WHERE pid = $1`, backendPID).Scan(&waitEventType)
		return err == nil && waitEventType.Valid && waitEventType.String == "Lock"
	}, 5*time.Second, 10*time.Millisecond, "backend %d did not block on the expected PostgreSQL lock", backendPID)
}

func newOpenAIAutoResetAccountLockIntegrationAccount(t *testing.T, label string) *service.Account {
	t.Helper()
	return newOpenAIAutoResetAccountLockIntegrationAccountWithProxy(t, label, nil)
}

func newOpenAIAutoResetAccountLockIntegrationProxiedAccount(
	t *testing.T,
	label string,
) (*service.Account, *service.Proxy) {
	t.Helper()
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name: fmt.Sprintf("auto-reset-lock-proxy-%s-%s", label, uuid.NewString()),
	})
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanupCtx, `DELETE FROM proxies WHERE id = $1`, proxy.ID)
		require.NoError(t, err)
	})

	return newOpenAIAutoResetAccountLockIntegrationAccountWithProxy(t, label, &proxy.ID), proxy
}

func newOpenAIAutoResetAccountLockIntegrationAccountWithProxy(
	t *testing.T,
	label string,
	proxyID *int64,
) *service.Account {
	t.Helper()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("auto-reset-lock-%s-%s", label, uuid.NewString()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     proxyID,
		Extra: map[string]any{
			service.OpenAIAutoResetCreditEnabledExtraKey: true,
		},
	})
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err)
	})
	return account
}

func openOpenAIAutoResetAccountLockIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", integrationPostgresDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(context.Background()))
	return db
}
