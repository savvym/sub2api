package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

const extendIdempotencyExpirationSQLPattern = `(?s)UPDATE idempotency_records\s+SET expires_at = GREATEST\(expires_at, \$2\),\s+updated_at = NOW\(\)\s+WHERE id = \$1\s+AND request_fingerprint = \$3`

func TestIdempotencyRepository_ExtendExpirationUsesMonotonicFingerprintGuard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expiresAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(extendIdempotencyExpirationSQLPattern).
		WithArgs(int64(42), expiresAt, "expected-fingerprint").
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := &idempotencyRepository{sql: db}
	extended, err := repo.ExtendExpiration(
		context.Background(),
		42,
		"expected-fingerprint",
		expiresAt,
	)
	require.NoError(t, err)
	require.True(t, extended)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIdempotencyRepository_ExtendExpirationReturnsFalseWhenFingerprintDoesNotMatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expiresAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(extendIdempotencyExpirationSQLPattern).
		WithArgs(int64(42), expiresAt, "stale-fingerprint").
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := &idempotencyRepository{sql: db}
	extended, err := repo.ExtendExpiration(
		context.Background(),
		42,
		"stale-fingerprint",
		expiresAt,
	)
	require.NoError(t, err)
	require.False(t, extended)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIdempotencyRepository_DeleteExpiredLocksCandidatesAndRechecksExpiration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	queryPattern := `(?s)WITH victims AS \(\s*SELECT id\s+FROM idempotency_records\s+WHERE expires_at <= \$1\s+ORDER BY expires_at ASC\s+LIMIT \$2\s+FOR UPDATE SKIP LOCKED\s*\)\s*DELETE FROM idempotency_records AS records\s+USING victims\s+WHERE records.id = victims.id\s+AND records.expires_at <= \$1`
	mock.ExpectExec(queryPattern).
		WithArgs(now, 25).
		WillReturnResult(sqlmock.NewResult(0, 3))

	repo := &idempotencyRepository{sql: db}
	deleted, err := repo.DeleteExpired(context.Background(), now, 25)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
