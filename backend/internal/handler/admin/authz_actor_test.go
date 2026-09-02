package admin

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminHandlerActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s adminHandlerActorStore) LoadSubjectSnapshot(context.Context, authz.SubjectRef) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s adminHandlerActorStore) LoadServicePrincipalSubjectSnapshotByCode(context.Context, string) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func adminHandlerTestActor(t testing.TB, kind authz.SubjectKind, id int64) authz.Actor {
	t.Helper()
	actor, err := buildAdminHandlerTestActor(kind, id)
	require.NoError(t, err)
	return actor
}

func buildAdminHandlerTestActor(kind authz.SubjectKind, id int64) (authz.Actor, error) {
	subject, err := authz.NewSubjectRef(kind, id)
	if err != nil {
		return authz.Actor{}, err
	}
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	if err != nil {
		return authz.Actor{}, err
	}
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       1,
		CurrentLegacyAdmin: kind == authz.SubjectKindUser,
		Configuration:      configuration,
	})
	if err != nil {
		return authz.Actor{}, err
	}
	resolver := authz.NewActorResolver(adminHandlerActorStore{snapshot: snapshot})
	if kind == authz.SubjectKindServicePrincipal {
		return resolver.ResolveServicePrincipal(
			context.Background(),
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
	}
	if kind != authz.SubjectKindUser {
		return authz.Actor{}, fmt.Errorf("unsupported test actor kind %q", kind)
	}
	return resolver.ResolveUser(context.Background(), id, authz.AuthMethodJWT)
}

func withAdminTestActor(t testing.TB, actor authz.Actor) gin.HandlerFunc {
	t.Helper()
	require.True(t, actor.Valid())
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
		if userID, isUser := actor.UserID(); isUser {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		}
		c.Next()
	}
}

func withAdminTestUserActorID(userID int64) gin.HandlerFunc {
	actor, err := buildAdminHandlerTestActor(authz.SubjectKindUser, userID)
	if err != nil {
		panic(fmt.Sprintf("build admin handler test actor: %v", err))
	}
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		c.Next()
	}
}
