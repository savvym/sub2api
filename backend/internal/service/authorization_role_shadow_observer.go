package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const authorizationRoleShadowLogComponent = "authorization.role_shadow.audit"

type authorizationRoleShadowLogObserver struct{}

var _ authz.RoleShadowObserver = (*authorizationRoleShadowLogObserver)(nil)

func NewAuthorizationRoleShadowObserver() authz.RoleShadowObserver {
	return &authorizationRoleShadowLogObserver{}
}

func (o *authorizationRoleShadowLogObserver) ObserveRoleShadow(_ context.Context, comparison authz.RoleShadowComparison) {
	if o == nil || !comparison.Valid() {
		return
	}
	log := logger.With(authorizationRoleShadowLogFields(comparison)...)
	if authorizationRoleShadowLogLevel(comparison) == logger.LevelWarn {
		log.Warn("authorization role shadow behavior mismatch")
		return
	}
	log.Info("authorization role shadow comparison")
}

func authorizationRoleShadowLogLevel(comparison authz.RoleShadowComparison) logger.Level {
	if comparison.BehaviorMismatch {
		return logger.LevelWarn
	}
	return logger.LevelInfo
}

func authorizationRoleShadowLogFields(comparison authz.RoleShadowComparison) []zap.Field {
	return []zap.Field{
		zap.String("component", authorizationRoleShadowLogComponent),
		zap.Int("schema_version", 1),
		zap.String("operation", string(comparison.Operation)),
		zap.String("subject_kind", string(comparison.SubjectKind)),
		zap.String("auth_method", string(comparison.AuthMethod)),
		zap.String("capability", string(comparison.Capability)),
		zap.String("resource_type", string(comparison.ResourceType)),
		zap.String("action", string(comparison.Action)),
		zap.String("authoritative_mode", string(authz.RoleAuthorizationModeLegacy)),
		zap.String("candidate_mode", string(authz.RoleAuthorizationModeRBAC)),
		zap.String("legacy_effect", string(comparison.Legacy.Effect)),
		zap.String("legacy_source", string(comparison.Legacy.Source)),
		zap.String("legacy_deny_reason", string(comparison.Legacy.DenyReason)),
		zap.String("rbac_effect", string(comparison.RBAC.Effect)),
		zap.String("rbac_source", string(comparison.RBAC.Source)),
		zap.String("rbac_deny_reason", string(comparison.RBAC.DenyReason)),
		zap.Bool("behavior_mismatch", comparison.BehaviorMismatch),
		zap.Bool("provenance_changed", comparison.ProvenanceChanged),
	}
}
