//go:build unit

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestAuthorizationRoleShadowLogSchemaIsRedactedAndLowCardinality(t *testing.T) {
	comparison := authz.RoleShadowComparison{
		Operation:        authz.PolicyOperationAuthorize,
		SubjectKind:      authz.SubjectKindUser,
		AuthMethod:       authz.AuthMethodJWT,
		ResourceType:     authz.ResourceTypeAccount,
		Action:           authz.ActionAccountDelete,
		Legacy:           authz.RoleShadowOutcome{Effect: authz.RoleShadowEffectAllow, Source: authz.MatchSourceLegacyAdmin},
		RBAC:             authz.RoleShadowOutcome{Effect: authz.RoleShadowEffectDeny, DenyReason: authz.DenyReasonNoMatchingAccess},
		BehaviorMismatch: true,
	}
	require.True(t, comparison.Valid())
	require.Equal(t, logger.LevelWarn, authorizationRoleShadowLogLevel(comparison))

	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range authorizationRoleShadowLogFields(comparison) {
		field.AddTo(encoder)
	}
	require.Equal(t, authorizationRoleShadowLogComponent, encoder.Fields["component"])
	require.Equal(t, int64(1), encoder.Fields["schema_version"])
	require.Equal(t, string(authz.PolicyOperationAuthorize), encoder.Fields["operation"])
	require.Equal(t, true, encoder.Fields["behavior_mismatch"])

	serialized, err := json.Marshal(encoder.Fields)
	require.NoError(t, err)
	text := string(serialized)
	for _, forbidden := range []string{
		"subject_id", "user_id", "service_principal_id", "resource_id",
		"account_id", "group_id", "role_id", "grant_id", "request_id",
		"987654321", "876543210",
	} {
		require.False(t, strings.Contains(text, forbidden), "shadow log leaked %q: %s", forbidden, text)
	}
}

func TestAuthorizationRoleShadowEquivalentDecisionUsesInfoLevel(t *testing.T) {
	comparison := authz.RoleShadowComparison{
		Operation:         authz.PolicyOperationCheckCapability,
		SubjectKind:       authz.SubjectKindUser,
		AuthMethod:        authz.AuthMethodJWT,
		Capability:        authz.CapabilityAPIKeyCreate,
		Legacy:            authz.RoleShadowOutcome{Effect: authz.RoleShadowEffectAllow, Source: authz.MatchSourceLegacyUser},
		RBAC:              authz.RoleShadowOutcome{Effect: authz.RoleShadowEffectAllow, Source: authz.MatchSourcePlatformCapability},
		ProvenanceChanged: true,
	}
	require.True(t, comparison.Valid())
	require.Equal(t, logger.LevelInfo, authorizationRoleShadowLogLevel(comparison))
}
