//go:build unit

package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTranslateHostingEntitlementTxError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantConflict bool
	}{
		{name: "serialization failure", err: &pq.Error{Code: "40001"}, wantConflict: true},
		{name: "deadlock", err: &pq.Error{Code: "40P01"}, wantConflict: true},
		{
			name: "initial entitlement insert race",
			err: &pq.Error{
				Code:       "23505",
				Constraint: hostingEntitlementUserUniqueConstraint,
			},
			wantConflict: true,
		},
		{
			name: "hoster assignment insert race",
			err: fmt.Errorf("wrapped: %w", &pq.Error{
				Code:       "23505",
				Constraint: hostingEntitlementUserRoleUniqueConstraint,
			}),
			wantConflict: true,
		},
		{
			name: "unrelated unique violation remains an infrastructure error",
			err: &pq.Error{
				Code:       "23505",
				Constraint: "unrelated_constraint",
			},
		},
		{name: "generic error remains unchanged", err: errors.New("database unavailable")},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := translateHostingEntitlementTxError(testCase.err)

			if testCase.wantConflict {
				require.ErrorIs(t, result, service.ErrHostingEntitlementConflict)
				require.ErrorIs(t, result, testCase.err)
				return
			}
			require.Same(t, testCase.err, result)
		})
	}
}
