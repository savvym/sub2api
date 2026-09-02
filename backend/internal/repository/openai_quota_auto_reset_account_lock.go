package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const openAIQuotaAutoResetAccountLockQuery = `
SELECT pg_try_advisory_xact_lock(
    hashtextextended('sub2api:openai-quota-auto-reset:account:' || $1::text, 0)
)`

const openAIQuotaAutoResetAccountRowLockQuery = `
SELECT id, proxy_id
FROM accounts
WHERE id = $1
  AND deleted_at IS NULL
FOR UPDATE`

const openAIQuotaAutoResetAccountProxyQuery = `
SELECT proxy_id
FROM accounts
WHERE id = $1
  AND deleted_at IS NULL`

const openAIQuotaAutoResetProxyRowLockQuery = `
SELECT id
FROM proxies
WHERE id = $1
  AND deleted_at IS NULL
FOR SHARE`

type openAIQuotaAutoResetAccountLocker struct {
	db *sql.DB
}

// NewOpenAIQuotaAutoResetAccountLocker creates the PostgreSQL-backed account
// lock used to serialize auto-reset side effects across application instances.
func NewOpenAIQuotaAutoResetAccountLocker(db *sql.DB) service.OpenAIQuotaAutoResetAccountLocker {
	return &openAIQuotaAutoResetAccountLocker{db: db}
}

func (l *openAIQuotaAutoResetAccountLocker) TryAcquire(
	ctx context.Context,
	accountID int64,
) (service.OpenAIQuotaAutoResetAccountLease, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("acquire OpenAI quota auto-reset account lock: nil context")
	}
	if accountID <= 0 {
		return nil, false, fmt.Errorf("acquire OpenAI quota auto-reset account lock: invalid account ID %d", accountID)
	}
	if l == nil || l.db == nil {
		return nil, false, errors.New("acquire OpenAI quota auto-reset account lock: nil database")
	}

	// The transaction owns the advisory lock. Its context must outlive request
	// cancellation so a reset already in flight cannot lose its lock early.
	lockCtx := context.WithoutCancel(ctx)
	tx, err := l.db.BeginTx(lockCtx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin OpenAI quota auto-reset account lock transaction: %w", err)
	}

	var acquired bool
	if err := tx.QueryRowContext(ctx, openAIQuotaAutoResetAccountLockQuery, accountID).Scan(&acquired); err != nil {
		rollbackErr := tx.Rollback()
		return nil, false, errors.Join(
			fmt.Errorf("query OpenAI quota auto-reset account lock: %w", err),
			wrapOpenAIQuotaAutoResetAccountLockRollbackError(rollbackErr),
		)
	}
	if !acquired {
		if err := tx.Rollback(); err != nil {
			return nil, false, fmt.Errorf("release unacquired OpenAI quota auto-reset account lock transaction: %w", err)
		}
		return nil, false, nil
	}

	return &openAIQuotaAutoResetAccountLease{tx: tx, accountID: accountID}, true, nil
}

func wrapOpenAIQuotaAutoResetAccountLockRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback OpenAI quota auto-reset account lock transaction: %w", err)
}

type openAIQuotaAutoResetAccountLease struct {
	tx        *sql.Tx
	accountID int64

	mu        sync.Mutex
	rowLocked bool
	released  bool
	err       error
}

func (l *openAIQuotaAutoResetAccountLease) LockAccountRow(ctx context.Context) (bool, error) {
	if l == nil {
		return false, errors.New("lock OpenAI quota auto-reset account row: nil lease")
	}
	if ctx == nil {
		return false, errors.New("lock OpenAI quota auto-reset account row: nil context")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.tx == nil {
		return false, errors.New("lock OpenAI quota auto-reset account row: lease is released")
	}
	if l.rowLocked {
		return true, nil
	}

	// Lock the selected proxy before the account. FOR SHARE blocks proxy content
	// updates while remaining compatible with the KEY SHARE lock used by account
	// proxy foreign keys. Rechecking proxy_id after the account row lock makes a
	// concurrent proxy switch fail closed without introducing account/proxy lock
	// inversion with the expiry sweep's proxy-then-account order.
	var expectedProxyID sql.NullInt64
	if err := l.tx.QueryRowContext(ctx, openAIQuotaAutoResetAccountProxyQuery, l.accountID).Scan(&expectedProxyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load OpenAI quota auto-reset account proxy: %w", err)
	}
	if expectedProxyID.Valid {
		var lockedProxyID int64
		if err := l.tx.QueryRowContext(ctx, openAIQuotaAutoResetProxyRowLockQuery, expectedProxyID.Int64).Scan(&lockedProxyID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, fmt.Errorf("lock OpenAI quota auto-reset proxy row: %w", err)
		}
		if lockedProxyID != expectedProxyID.Int64 {
			return false, fmt.Errorf("lock OpenAI quota auto-reset proxy row: proxy ID mismatch: got %d, want %d", lockedProxyID, expectedProxyID.Int64)
		}
	}

	// The account row lock is the shared exclusion boundary with every account
	// writer, including direct SQL and bulk updates. The advisory-only phase lets
	// the worker publish its claim and resetting state without blocking itself;
	// this upgrade happens only immediately before the external reset.
	var lockedAccountID int64
	var lockedProxyID sql.NullInt64
	if err := l.tx.QueryRowContext(ctx, openAIQuotaAutoResetAccountRowLockQuery, l.accountID).Scan(&lockedAccountID, &lockedProxyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock OpenAI quota auto-reset account row: %w", err)
	}
	if lockedAccountID != l.accountID {
		return false, fmt.Errorf("lock OpenAI quota auto-reset account row: account ID mismatch: got %d, want %d", lockedAccountID, l.accountID)
	}
	if lockedProxyID.Valid != expectedProxyID.Valid || (lockedProxyID.Valid && lockedProxyID.Int64 != expectedProxyID.Int64) {
		return false, nil
	}
	l.rowLocked = true
	return true, nil
}

func (l *openAIQuotaAutoResetAccountLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return l.err
	}
	l.released = true
	if l.tx != nil {
		l.err = l.tx.Rollback()
	}
	return l.err
}
