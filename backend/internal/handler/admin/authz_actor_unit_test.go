//go:build unit

package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func attachAdminTestUserActorID(t testing.TB, c *gin.Context, userID int64) {
	t.Helper()
	actor := adminHandlerTestActor(t, authz.SubjectKindUser, userID)
	c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
}
