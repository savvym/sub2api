package migrations

import (
	"strings"
	"testing"

	entschema "entgo.io/ent/dialect/sql/schema"
	entmigrate "github.com/Wei-Shaw/sub2api/ent/migrate"
	"github.com/stretchr/testify/require"
)

func TestUserHostingEntitlementsMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("244_user_hosting_entitlements.sql")
	require.NoError(t, err)

	sql := string(content)
	compact := strings.Join(strings.Fields(sql), " ")

	require.Contains(t, compact, "CREATE TABLE IF NOT EXISTS user_hosting_entitlements")
	require.Contains(t, compact, "CONSTRAINT user_hosting_entitlements_user_id_key UNIQUE (user_id)")
	require.Contains(t, compact, "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE")
	require.Contains(t, compact, "FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT")
	require.Contains(t, compact, "FOREIGN KEY (updated_by_user_id) REFERENCES users(id) ON DELETE RESTRICT")
	require.Contains(t, compact, "CHECK (account_limit >= 0)")
	require.Contains(t, compact, "CHECK (group_limit >= 0)")
	require.Contains(t, compact, "CHECK (version > 0)")
	require.Contains(t, compact, "account_limit BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, compact, "group_limit BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, compact, "version BIGINT NOT NULL DEFAULT 1")

	// Qualification remains exclusively in user_roles and this migration must
	// not enable any runtime capability or authorization mode.
	require.NotContains(t, compact, "INSERT INTO user_roles")
	require.NotContains(t, compact, "INSERT INTO roles")
	require.NotContains(t, compact, "INSERT INTO settings")
	require.NotContains(t, sql, "self_service_hosting_enabled")
	require.NotContains(t, sql, "role_authorization_mode")
}

func TestUserHostingEntitlementsGeneratedSchemaContract(t *testing.T) {
	table := entmigrate.UserHostingEntitlementsTable
	require.Equal(t, "user_hosting_entitlements", table.Name)
	require.Len(t, table.ForeignKeys, 3)

	var userIDUnique bool
	for _, column := range table.Columns {
		if column.Name == "user_id" {
			userIDUnique = column.Unique
			break
		}
	}
	require.True(t, userIDUnique, "user_id should remain unique through the one-to-one edge")

	expected := map[string]struct {
		symbol   string
		onDelete entschema.ReferenceOption
	}{
		"user_id": {
			symbol:   "user_hosting_entitlements_user_id_fkey",
			onDelete: entschema.Cascade,
		},
		"created_by_user_id": {
			symbol:   "user_hosting_entitlements_created_by_user_id_fkey",
			onDelete: entschema.Restrict,
		},
		"updated_by_user_id": {
			symbol:   "user_hosting_entitlements_updated_by_user_id_fkey",
			onDelete: entschema.Restrict,
		},
	}
	for _, foreignKey := range table.ForeignKeys {
		require.Len(t, foreignKey.Columns, 1)
		column := foreignKey.Columns[0].Name
		contract, ok := expected[column]
		require.True(t, ok, "unexpected foreign key column %s", column)
		require.Equal(t, contract.symbol, foreignKey.Symbol)
		require.Equal(t, contract.onDelete, foreignKey.OnDelete)
		delete(expected, column)
	}
	require.Empty(t, expected)
}
