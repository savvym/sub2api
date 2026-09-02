//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestGroupOwnerScopedNameUniqueMigrationPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	for _, indexName := range []string{
		"idx_groups_platform_name_unique_active",
		"idx_groups_owner_name_unique_active",
	} {
		var (
			valid      bool
			unique     bool
			definition string
		)
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT index_row.indisvalid, index_row.indisunique, pg_get_indexdef(index_row.indexrelid)
FROM pg_index AS index_row
JOIN pg_class AS index_class ON index_class.oid = index_row.indexrelid
WHERE index_class.relname = $1`, indexName).Scan(&valid, &unique, &definition))
		require.True(t, valid)
		require.True(t, unique)
		require.Contains(t, strings.ToLower(definition), "lower((name)::text)")
		require.Contains(t, strings.ToLower(definition), "deleted_at is null")
	}

	var legacyIndex sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT indexname
FROM pg_indexes
WHERE schemaname = 'public' AND indexname = 'groups_name_unique_active'`).Scan(&legacyIndex)
	require.ErrorIs(t, err, sql.ErrNoRows)

	suffix := time.Now().UnixNano()
	ownerOne := insertGroupNameOwnerTestUser(t, ctx, db, fmt.Sprintf("group-name-owner-one-%d@example.com", suffix))
	ownerTwo := insertGroupNameOwnerTestUser(t, ctx, db, fmt.Sprintf("group-name-owner-two-%d@example.com", suffix))

	_, err = db.ExecContext(ctx, `
INSERT INTO groups (name, platform, status)
VALUES ('Shared Name', 'openai', 'active')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO groups (name, platform, status)
VALUES ('shared name', 'anthropic', 'active')`)
	require.Error(t, err, "active platform groups must be globally unique by folded name")

	var ownerOneGroupID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (
    name, platform, status, owner_user_id, created_by_user_id
)
VALUES ('Shared Name', 'openai', 'active', $1, $1)
RETURNING id`, ownerOne).Scan(&ownerOneGroupID))
	_, err = db.ExecContext(ctx, `
INSERT INTO groups (
    name, platform, status, owner_user_id, created_by_user_id
)
VALUES ('SHARED NAME', 'gemini', 'active', $1, $1)`, ownerOne)
	require.Error(t, err, "one owner must not have folded-name duplicates")

	_, err = db.ExecContext(ctx, `
INSERT INTO groups (
    name, platform, status, owner_user_id, created_by_user_id
)
VALUES ('shared name', 'anthropic', 'active', $1, $1)`, ownerTwo)
	require.NoError(t, err, "different owners may reuse the same group name")

	_, err = db.ExecContext(ctx, `
UPDATE groups SET deleted_at = statement_timestamp() WHERE id = $1`, ownerOneGroupID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO groups (
    name, platform, status, owner_user_id, created_by_user_id
)
VALUES ('shared name', 'gemini', 'active', $1, $1)`, ownerOne)
	require.NoError(t, err, "soft-deleted tenant names may be reused")
}

func insertGroupNameOwnerTestUser(
	t testing.TB,
	ctx context.Context,
	db *sql.DB,
	email string,
) int64 {
	t.Helper()
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ($1, 'integration-test-hash', 'user', 'active')
RETURNING id`, email).Scan(&userID))
	return userID
}
