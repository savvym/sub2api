package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdempotencyExpiryDeleteGuardMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("236_idempotency_expiry_delete_guard.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, compact, "IF OLD.expires_at > CURRENT_TIMESTAMP THEN RETURN NULL")
	require.Contains(t, compact, "BEFORE DELETE ON idempotency_records")
	require.Contains(t, compact, "FOR EACH ROW EXECUTE FUNCTION guard_idempotency_record_expiry_delete()")
}
