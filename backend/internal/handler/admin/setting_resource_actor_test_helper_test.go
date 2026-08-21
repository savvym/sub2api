package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func attachSettingAdminTestActor(t testing.TB, c *gin.Context) {
	t.Helper()
	actor := adminHandlerTestActor(t, authz.SubjectKindUser, 1)
	c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
}
