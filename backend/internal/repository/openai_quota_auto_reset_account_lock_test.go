package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaAutoResetAccountLockerAcquireAndRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}).AddRow(nil))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountRowLockQuery)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "proxy_id"}).AddRow(int64(42), nil))
	mock.ExpectRollback()

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(requestCtx, 42)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	exists, err := lease.LockAccountRow(requestCtx)
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = lease.LockAccountRow(requestCtx)
	require.NoError(t, err)
	require.True(t, exists, "row-lock upgrade must be idempotent")

	cancelRequest()
	require.NoError(t, lease.Release(), "request cancellation must not end the lock transaction")
	require.NoError(t, lease.Release(), "release must be idempotent")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLockerReleaseIsConcurrentSafe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
		WithArgs(int64(43)).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectRollback()

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 43)
	require.NoError(t, err)
	require.True(t, acquired)

	const callers = 16
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- lease.Release()
		}()
	}
	wg.Wait()
	close(errs)
	for releaseErr := range errs {
		require.NoError(t, releaseErr)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLockerContention(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
		WithArgs(int64(44)).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))
	mock.ExpectRollback()

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 44)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, lease)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLeaseProtectsConfiguredProxy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
		WithArgs(int64(51)).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
		WithArgs(int64(51)).
		WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetProxyRowLockQuery)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountRowLockQuery)).
		WithArgs(int64(51)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "proxy_id"}).AddRow(int64(51), int64(7)))
	mock.ExpectRollback()

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 51)
	require.NoError(t, err)
	require.True(t, acquired)
	exists, err := lease.LockAccountRow(context.Background())
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, lease.Release())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLeaseRejectsConcurrentProxySwitch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
		WithArgs(int64(52)).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
		WithArgs(int64(52)).
		WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetProxyRowLockQuery)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountRowLockQuery)).
		WithArgs(int64(52)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "proxy_id"}).AddRow(int64(52), int64(8)))
	mock.ExpectRollback()

	lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 52)
	require.NoError(t, err)
	require.True(t, acquired)
	exists, err := lease.LockAccountRow(context.Background())
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, lease.Release())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLockerErrors(t *testing.T) {
	t.Run("cancelled acquisition rolls back detached transaction", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(ctx, 45)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, acquired)
		require.Nil(t, lease)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		beginErr := errors.New("begin failed")
		mock.ExpectBegin().WillReturnError(beginErr)

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 45)
		require.ErrorIs(t, err, beginErr)
		require.False(t, acquired)
		require.Nil(t, lease)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("advisory query rolls back", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		queryErr := errors.New("query failed")
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
			WithArgs(int64(46)).
			WillReturnError(queryErr)
		mock.ExpectRollback()

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 46)
		require.ErrorIs(t, err, queryErr)
		require.False(t, acquired)
		require.Nil(t, lease)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("account row lock query fails and release rolls back", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		queryErr := errors.New("row lock failed")
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
			WithArgs(int64(49)).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
			WithArgs(int64(49)).
			WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}).AddRow(nil))
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountRowLockQuery)).
			WithArgs(int64(49)).
			WillReturnError(queryErr)
		mock.ExpectRollback()

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 49)
		require.NoError(t, err)
		require.True(t, acquired)
		require.NotNil(t, lease)
		exists, err := lease.LockAccountRow(context.Background())
		require.ErrorIs(t, err, queryErr)
		require.False(t, exists)
		require.NoError(t, lease.Release())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing account fails closed", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
			WithArgs(int64(50)).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
			WithArgs(int64(50)).
			WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}))
		mock.ExpectRollback()

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 50)
		require.NoError(t, err)
		require.True(t, acquired)
		require.NotNil(t, lease)
		exists, err := lease.LockAccountRow(context.Background())
		require.NoError(t, err)
		require.False(t, exists)
		require.NoError(t, lease.Release())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unacquired rollback", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		rollbackErr := errors.New("rollback failed")
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
			WithArgs(int64(47)).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))
		mock.ExpectRollback().WillReturnError(rollbackErr)

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 47)
		require.ErrorIs(t, err, rollbackErr)
		require.False(t, acquired)
		require.Nil(t, lease)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("release", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		rollbackErr := errors.New("connection lost")
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountLockQuery)).
			WithArgs(int64(48)).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountProxyQuery)).
			WithArgs(int64(48)).
			WillReturnRows(sqlmock.NewRows([]string{"proxy_id"}).AddRow(nil))
		mock.ExpectQuery(regexp.QuoteMeta(openAIQuotaAutoResetAccountRowLockQuery)).
			WithArgs(int64(48)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "proxy_id"}).AddRow(int64(48), nil))
		mock.ExpectRollback().WillReturnError(rollbackErr)

		lease, acquired, err := NewOpenAIQuotaAutoResetAccountLocker(db).TryAcquire(context.Background(), 48)
		require.NoError(t, err)
		require.True(t, acquired)
		exists, err := lease.LockAccountRow(context.Background())
		require.NoError(t, err)
		require.True(t, exists)
		require.ErrorIs(t, lease.Release(), rollbackErr)
		require.ErrorIs(t, lease.Release(), rollbackErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOpenAIQuotaAutoResetAccountLockerValidatesInput(t *testing.T) {
	validDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = validDB.Close() })

	tests := []struct {
		name      string
		locker    *openAIQuotaAutoResetAccountLocker
		ctx       context.Context
		accountID int64
		wantError string
	}{
		{name: "nil receiver", ctx: context.Background(), accountID: 1, wantError: "nil database"},
		{name: "nil database", locker: &openAIQuotaAutoResetAccountLocker{}, ctx: context.Background(), accountID: 1, wantError: "nil database"},
		{name: "nil context", locker: &openAIQuotaAutoResetAccountLocker{db: validDB}, accountID: 1, wantError: "nil context"},
		{name: "zero account", locker: &openAIQuotaAutoResetAccountLocker{db: validDB}, ctx: context.Background(), wantError: "invalid account ID 0"},
		{name: "negative account", locker: &openAIQuotaAutoResetAccountLocker{db: validDB}, ctx: context.Background(), accountID: -1, wantError: "invalid account ID -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease, acquired, err := tt.locker.TryAcquire(tt.ctx, tt.accountID)
			require.ErrorContains(t, err, tt.wantError)
			require.False(t, acquired)
			require.Nil(t, lease)
		})
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetAccountLeaseNilRelease(t *testing.T) {
	var lease *openAIQuotaAutoResetAccountLease
	require.NoError(t, lease.Release())

	lease = &openAIQuotaAutoResetAccountLease{tx: (*sql.Tx)(nil)}
	require.NoError(t, lease.Release())
	exists, err := lease.LockAccountRow(context.Background())
	require.Error(t, err)
	require.False(t, exists)
}
