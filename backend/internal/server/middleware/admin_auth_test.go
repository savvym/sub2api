//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminAuthJWTValidatesTokenVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)

	admin := &service.User{
		ID:           1,
		Email:        "admin@example.com",
		Role:         service.RoleAdmin,
		Status:       service.StatusActive,
		TokenVersion: 2,
		Concurrency:  1,
	}

	userRepo := &stubUserRepo{
		getByID: func(ctx context.Context, id int64) (*service.User, error) {
			if id != admin.ID {
				return nil, service.ErrUserNotFound
			}
			clone := *admin
			return &clone, nil
		},
	}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	actorResolver, _ := newMiddlewareActorResolver(t, map[int64]*service.User{admin.ID: admin})

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authService, userService, nil, nil, actorResolver)))
	router.GET("/t", func(c *gin.Context) {
		actor, hasActor := authz.ActorFromContext(c.Request.Context())
		actorUserID, _ := actor.UserID()
		c.JSON(http.StatusOK, gin.H{
			"ok":                true,
			"has_actor":         hasActor,
			"actor_user_id":     actorUserID,
			"actor_auth_method": actor.AuthMethod(),
		})
	})

	t.Run("token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"has_actor":true`)
		require.Contains(t, w.Body.String(), `"actor_user_id":1`)
		require.Contains(t, w.Body.String(), `"actor_auth_method":"jwt"`)
	})

	t.Run("websocket_token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("websocket_token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"has_actor":true`)
		require.Contains(t, w.Body.String(), `"actor_user_id":1`)
		require.Contains(t, w.Body.String(), `"actor_auth_method":"jwt"`)
	})
}

func TestAdminAuthJWTUsesCurrentResolverSnapshotForAdminGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	compatibilityUser := &service.User{
		ID:           5,
		Email:        "admin@example.com",
		Role:         service.RoleAdmin,
		Status:       service.StatusActive,
		TokenVersion: 1,
	}
	userRepo := &stubUserRepo{getByID: func(_ context.Context, _ int64) (*service.User, error) {
		clone := *compatibilityUser
		return &clone, nil
	}}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	actorResolver, actorStore := newMiddlewareActorResolver(t, map[int64]*service.User{compatibilityUser.ID: compatibilityUser})
	actorStore.userSnapshots[compatibilityUser.ID] = mustMiddlewareSubjectSnapshot(
		t,
		authz.SubjectKindUser,
		compatibilityUser.ID,
		true,
		true,
		false,
	)

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authService, userService, nil, nil, actorResolver)))
	router.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })
	token, err := authService.GenerateToken(context.Background(), compatibilityUser)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "FORBIDDEN")
}

func TestAdminAuthJWTAuthorizationUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	admin := &service.User{
		ID:           6,
		Email:        "admin@example.com",
		Role:         service.RoleAdmin,
		Status:       service.StatusActive,
		TokenVersion: 1,
	}
	userRepo := &stubUserRepo{getByID: func(_ context.Context, _ int64) (*service.User, error) {
		clone := *admin
		return &clone, nil
	}}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	actorResolver, actorStore := newMiddlewareActorResolver(t, map[int64]*service.User{admin.ID: admin})
	actorStore.userErr = errors.New("database unavailable")

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authService, userService, nil, nil, actorResolver)))
	router.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })
	token, err := authService.GenerateToken(context.Background(), admin)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "AUTHORIZATION_UNAVAILABLE")
}

type stubAdminAPIKeyReader struct {
	key string
	err error
}

func (s stubAdminAPIKeyReader) GetAdminAPIKey(context.Context) (string, error) {
	return s.key, s.err
}

type stubFirstAdminReader struct {
	admin *service.User
	err   error
	calls int
}

func (s *stubFirstAdminReader) GetFirstAdmin(context.Context) (*service.User, error) {
	s.calls++
	return s.admin, s.err
}

func TestValidateAdminAPIKeySetsServicePrincipalActorAndCompatibilitySubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &service.User{
		ID:          1,
		Email:       "admin@example.com",
		Role:        service.RoleAdmin,
		Status:      service.StatusActive,
		Concurrency: 3,
	}
	actorResolver, actorStore := newMiddlewareActorResolver(t, map[int64]*service.User{admin.ID: admin})
	actorStore.setServicePrincipal(t, authz.AdminAPIKeyServicePrincipalCode, 91, true, true)
	adminReader := &stubFirstAdminReader{admin: admin}

	router := gin.New()
	router.GET("/t", func(c *gin.Context) {
		if !validateAdminAPIKey(c, c.GetHeader("x-api-key"), stubAdminAPIKeyReader{key: "secret"}, adminReader, actorResolver) {
			return
		}
		actor, hasActor := authz.ActorFromContext(c.Request.Context())
		principalID, isPrincipal := actor.ServicePrincipalID()
		subject, hasSubject := GetAuthSubjectFromContext(c)
		c.JSON(http.StatusOK, gin.H{
			"has_actor":         hasActor,
			"is_principal":      isPrincipal,
			"principal_id":      principalID,
			"actor_auth_method": actor.AuthMethod(),
			"has_subject":       hasSubject,
			"subject_user_id":   subject.UserID,
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", "secret")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"has_actor":true`)
	require.Contains(t, w.Body.String(), `"is_principal":true`)
	require.Contains(t, w.Body.String(), `"principal_id":91`)
	require.Contains(t, w.Body.String(), `"actor_auth_method":"admin_api_key"`)
	require.Contains(t, w.Body.String(), `"has_subject":true`)
	require.Contains(t, w.Body.String(), `"subject_user_id":1`)
	require.Equal(t, 1, actorStore.servicePrincipalCalls)
	require.Equal(t, 1, adminReader.calls)
}

func TestValidateAdminAPIKeyRejectsMissingAndDisabledPrincipalIdentically(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var responses []string
	for _, testCase := range []struct {
		name   string
		exists bool
	}{
		{name: "missing", exists: false},
		{name: "disabled", exists: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actorResolver, actorStore := newMiddlewareActorResolver(t, nil)
			actorStore.setServicePrincipal(t, authz.AdminAPIKeyServicePrincipalCode, 92, testCase.exists, false)
			adminReader := &stubFirstAdminReader{admin: &service.User{ID: 1}}
			router := gin.New()
			router.GET("/t", func(c *gin.Context) {
				if validateAdminAPIKey(c, c.GetHeader("x-api-key"), stubAdminAPIKeyReader{key: "secret"}, adminReader, actorResolver) {
					c.Status(http.StatusOK)
				}
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.Header.Set("x-api-key", "secret")
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code)
			require.Contains(t, w.Body.String(), "INVALID_ADMIN_KEY")
			require.Equal(t, 1, actorStore.servicePrincipalCalls)
			require.Zero(t, adminReader.calls)
			responses = append(responses, w.Body.String())
		})
	}
	require.Equal(t, responses[0], responses[1])
}

func TestValidateAdminAPIKeyAuthorizationUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actorResolver, actorStore := newMiddlewareActorResolver(t, nil)
	actorStore.servicePrincipalErr = errors.New("database unavailable")
	adminReader := &stubFirstAdminReader{admin: &service.User{ID: 1}}
	router := gin.New()
	router.GET("/t", func(c *gin.Context) {
		if validateAdminAPIKey(c, c.GetHeader("x-api-key"), stubAdminAPIKeyReader{key: "secret"}, adminReader, actorResolver) {
			c.Status(http.StatusOK)
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", "secret")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "AUTHORIZATION_UNAVAILABLE")
	require.Zero(t, adminReader.calls)
}

func TestValidateAdminAPIKeyRejectsWrongKeyBeforeResolvingActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actorResolver, actorStore := newMiddlewareActorResolver(t, nil)
	actorStore.setServicePrincipal(t, authz.AdminAPIKeyServicePrincipalCode, 93, true, true)
	adminReader := &stubFirstAdminReader{admin: &service.User{ID: 1}}
	router := gin.New()
	router.GET("/t", func(c *gin.Context) {
		if validateAdminAPIKey(c, c.GetHeader("x-api-key"), stubAdminAPIKeyReader{key: "secret"}, adminReader, actorResolver) {
			c.Status(http.StatusOK)
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", "wrong")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "INVALID_ADMIN_KEY")
	require.Zero(t, actorStore.servicePrincipalCalls)
	require.Zero(t, adminReader.calls)
}

type stubUserRepo struct {
	getByID func(ctx context.Context, id int64) (*service.User, error)
}

func (s *stubUserRepo) Create(ctx context.Context, user *service.User) error {
	panic("unexpected Create call")
}

func (s *stubUserRepo) CreateWithEmailAliasGuard(ctx context.Context, user *service.User) error {
	panic("unexpected CreateWithEmailAliasGuard call")
}

func (s *stubUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	if s.getByID == nil {
		panic("GetByID not stubbed")
	}
	return s.getByID(ctx, id)
}

func (s *stubUserRepo) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	panic("unexpected GetByEmail call")
}

func (s *stubUserRepo) GetFirstAdmin(ctx context.Context) (*service.User, error) {
	panic("unexpected GetFirstAdmin call")
}

func (s *stubUserRepo) Update(ctx context.Context, user *service.User, fields service.UserUpdateFields) error {
	panic("unexpected Update call")
}

func (s *stubUserRepo) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *stubUserRepo) GetUserAvatar(ctx context.Context, userID int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (s *stubUserRepo) UpsertUserAvatar(ctx context.Context, userID int64, input service.UpsertUserAvatarInput) (*service.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

func (s *stubUserRepo) DeleteUserAvatar(ctx context.Context, userID int64) error {
	panic("unexpected DeleteUserAvatar call")
}

func (s *stubUserRepo) List(ctx context.Context, params pagination.PaginationParams) ([]service.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *stubUserRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters service.UserListFilters) ([]service.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *stubUserRepo) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserIDs call")
}

func (s *stubUserRepo) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserID call")
}

func (s *stubUserRepo) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	panic("unexpected UpdateUserLastActiveAt call")
}

func (s *stubUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *stubUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected DeductBalance call")
}

func (s *stubUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (service.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *stubUserRepo) SetBalance(ctx context.Context, id int64, value float64) (service.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

func (s *stubUserRepo) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *stubUserRepo) BatchSetConcurrency(context.Context, []int64, int) (int, error) { return 0, nil }
func (s *stubUserRepo) BatchAddConcurrency(context.Context, []int64, int) (int, error) { return 0, nil }
func (s *stubUserRepo) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

func (s *stubUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	panic("unexpected ExistsByEmail call")
}

func (s *stubUserRepo) ExistsByEmailAlias(ctx context.Context, email string) (bool, error) {
	panic("unexpected ExistsByEmailAlias call")
}

func (s *stubUserRepo) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *stubUserRepo) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *stubUserRepo) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *stubUserRepo) ListUserAuthIdentities(ctx context.Context, userID int64) ([]service.UserAuthIdentityRecord, error) {
	panic("unexpected ListUserAuthIdentities call")
}

func (s *stubUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

func (s *stubUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

func (s *stubUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

func (s *stubUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

func (s *stubUserRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*service.User, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}
