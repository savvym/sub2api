//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const roleCachePostgresAdminDSNEnv = "SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN"

func TestRoleAuthorizationCacheInvalidationPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	migrationSQL, err := dbmigrations.FS.ReadFile("233_role_authorization_cache_invalidation.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration 233 must be safe to reapply")

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, status)
		VALUES ('role-cache-invalidation@example.test', 'not-a-real-password-hash', 'user', 'active')
		RETURNING id
	`).Scan(&userID))

	keys := []string{
		"sk-role-cache-invalidation-first",
		"sk-role-cache-invalidation-second",
	}
	for index, key := range keys {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO api_keys (user_id, key, name, status)
			VALUES ($1, $2, $3, 'active')
		`, userID, key, fmt.Sprintf("role-cache-key-%d", index+1))
		require.NoError(t, err)
	}
	require.NoError(t, clearRoleCacheOutbox(ctx, tx))

	t.Run("authz version only enqueues every live key once", func(t *testing.T) {
		_, err := tx.ExecContext(ctx, `
			UPDATE users
			SET authz_version = authz_version + 1
			WHERE id = $1
		`, userID)
		require.NoError(t, err)
		assertRoleCacheOutbox(t, ctx, tx, keys)
		require.NoError(t, clearRoleCacheOutbox(ctx, tx))
	})

	t.Run("unrelated user update does not enqueue", func(t *testing.T) {
		_, err := tx.ExecContext(ctx, `
			UPDATE users
			SET balance = balance + 1
			WHERE id = $1
		`, userID)
		require.NoError(t, err)

		var count int
		require.NoError(t, tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM auth_cache_invalidation_outbox`,
		).Scan(&count))
		require.Zero(t, count)
	})

	t.Run("role and version update enqueues one row per key", func(t *testing.T) {
		_, err := tx.ExecContext(ctx, `
			UPDATE users
			SET role = 'admin', authz_version = authz_version + 1
			WHERE id = $1
		`, userID)
		require.NoError(t, err)
		assertRoleCacheOutbox(t, ctx, tx, keys)
	})
}

func assertRoleCacheOutbox(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	plaintextKeys []string,
) {
	t.Helper()

	rows, err := tx.QueryContext(ctx, `
		SELECT cache_key, COUNT(*)
		FROM auth_cache_invalidation_outbox
		GROUP BY cache_key
		ORDER BY cache_key
	`)
	require.NoError(t, err)
	defer rows.Close()

	actual := make(map[string]int)
	for rows.Next() {
		var cacheKey string
		var count int
		require.NoError(t, rows.Scan(&cacheKey, &count))
		actual[cacheKey] = count
		for _, plaintext := range plaintextKeys {
			require.NotEqual(t, plaintext, cacheKey)
			require.NotContains(t, cacheKey, plaintext)
		}
	}
	require.NoError(t, rows.Err())

	expected := make(map[string]int, len(plaintextKeys))
	for _, plaintext := range plaintextKeys {
		sum := sha256.Sum256([]byte(plaintext))
		expected[hex.EncodeToString(sum[:])] = 1
	}
	require.Equal(t, expected, actual)
}

func clearRoleCacheOutbox(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `TRUNCATE auth_cache_invalidation_outbox`)
	return err
}

func newRoleCachePostgresTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv(roleCachePostgresAdminDSNEnv))
	if adminDSN == "" {
		t.Skipf("set %s to run the isolated PostgreSQL migration test", roleCachePostgresAdminDSNEnv)
	}
	parsed, err := url.Parse(adminDSN)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		t.Fatalf("%s must be a PostgreSQL URL", roleCachePostgresAdminDSNEnv)
	}

	adminDB, err := sql.Open("postgres", adminDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminDB.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, adminDB.PingContext(ctx))

	databaseName := fmt.Sprintf("sub2api_role_cache_it_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = adminDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(databaseName))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, dropErr := adminDB.ExecContext(
			cleanupCtx,
			"DROP DATABASE "+pq.QuoteIdentifier(databaseName)+" WITH (FORCE)",
		)
		require.NoError(t, dropErr)
	})

	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	testDB, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = testDB.Close() })
	require.NoError(t, testDB.PingContext(ctx))
	return testDB
}
