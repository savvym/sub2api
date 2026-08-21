package admin

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type settingHandlerActorRepoStub struct {
	values map[string]string
	calls  int
}

func (s *settingHandlerActorRepoStub) Get(context.Context, string) (*service.Setting, error) {
	s.calls++
	return nil, service.ErrSettingNotFound
}

func (s *settingHandlerActorRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.calls++
	return s.values[key], nil
}

func (s *settingHandlerActorRepoStub) Set(context.Context, string, string) error {
	s.calls++
	return nil
}

func (s *settingHandlerActorRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.calls++
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingHandlerActorRepoStub) SetMultiple(_ context.Context, updates map[string]string) error {
	s.calls++
	if s.values == nil {
		s.values = make(map[string]string, len(updates))
	}
	for key, value := range updates {
		s.values[key] = value
	}
	return nil
}

func (s *settingHandlerActorRepoStub) GetAll(context.Context) (map[string]string, error) {
	s.calls++
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingHandlerActorRepoStub) Delete(context.Context, string) error {
	s.calls++
	return nil
}

func setupRedeemSettingResourceActorRouter(actor *authz.Actor, compatibilityUserID int64) (*gin.Engine, *stubAdminService, *settingHandlerActorRepoStub) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != nil {
			c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), *actor))
		}
		if compatibilityUserID > 0 {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: compatibilityUserID})
		}
		c.Next()
	})

	adminService := newStubAdminService()
	settingRepo := &settingHandlerActorRepoStub{values: map[string]string{
		service.SettingKeyDefaultSubscriptions:                `[{"group_id":17,"validity_days":30}]`,
		service.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"group_id":19,"validity_days":14}]`,
	}}
	settingService := service.NewSettingService(settingRepo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	redeemHandler := NewRedeemHandler(adminService, nil)
	settingHandler := NewSettingHandler(settingService, nil, nil, nil, nil, nil, nil)

	router.GET("/redeem-codes", redeemHandler.List)
	router.GET("/redeem-codes/export", redeemHandler.Export)
	router.GET("/redeem-codes/:id", redeemHandler.GetByID)
	router.DELETE("/redeem-codes/:id", redeemHandler.Delete)
	router.POST("/redeem-codes/batch-delete", redeemHandler.BatchDelete)
	router.POST("/redeem-codes/:id/expire", redeemHandler.Expire)
	router.GET("/settings", settingHandler.GetSettings)
	router.PUT("/settings", settingHandler.UpdateSettings)
	return router, adminService, settingRepo
}

type redeemSettingResourceRequest struct {
	method string
	path   string
	body   string
}

func performRedeemSettingResourceRequest(router *gin.Engine, request redeemSettingResourceRequest) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
	if request.body != "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestRedeemAndSettingHandlersRejectMissingOrMalformedActorBeforeInputParsing(t *testing.T) {
	malformedActor := authz.Actor{}
	for _, testCase := range []struct {
		name  string
		actor *authz.Actor
	}{
		{name: "missing actor"},
		{name: "malformed actor", actor: &malformedActor},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, adminService, settingRepo := setupRedeemSettingResourceActorRouter(testCase.actor, 1)
			requests := []redeemSettingResourceRequest{
				{method: http.MethodGet, path: "/redeem-codes"},
				{method: http.MethodGet, path: "/redeem-codes/not-an-id"},
				{method: http.MethodDelete, path: "/redeem-codes/not-an-id"},
				{method: http.MethodPost, path: "/redeem-codes/batch-delete", body: `{`},
				{method: http.MethodPost, path: "/redeem-codes/not-an-id/expire"},
				{method: http.MethodGet, path: "/redeem-codes/export"},
				{method: http.MethodGet, path: "/settings"},
				{method: http.MethodPut, path: "/settings", body: `{`},
			}

			for _, request := range requests {
				recorder := performRedeemSettingResourceRequest(router, request)
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
				require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
			}
			require.Zero(t, adminService.resourceActorCalls)
			require.Zero(t, adminService.lastListRedeemCodes.calls)
			require.Zero(t, settingRepo.calls)
		})
	}
}

func TestRedeemAndSettingHandlersAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{name: "jwt user", actor: adminHandlerTestActor(t, authz.SubjectKindUser, 41), compatibilityUserID: 41},
		{name: "admin api key service principal", actor: adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73), compatibilityUserID: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, adminService, settingRepo := setupRedeemSettingResourceActorRouter(&testCase.actor, testCase.compatibilityUserID)
			requests := []redeemSettingResourceRequest{
				{method: http.MethodGet, path: "/redeem-codes"},
				{method: http.MethodGet, path: "/redeem-codes/5"},
				{method: http.MethodDelete, path: "/redeem-codes/5"},
				{method: http.MethodPost, path: "/redeem-codes/batch-delete", body: `{"ids":[5]}`},
				{method: http.MethodPost, path: "/redeem-codes/5/expire"},
				{method: http.MethodGet, path: "/redeem-codes/export"},
				{method: http.MethodGet, path: "/settings"},
				{method: http.MethodPut, path: "/settings", body: `{"default_subscriptions":[{"group_id":17,"validity_days":30}],"auth_source_default_email_subscriptions":[{"group_id":19,"validity_days":14}]}`},
			}

			for _, request := range requests {
				recorder := performRedeemSettingResourceRequest(router, request)
				require.Equal(t, http.StatusOK, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
			}
			require.Equal(t, 6, adminService.resourceActorCalls)
			wantKey, ok := testCase.actor.SubjectKey()
			require.True(t, ok)
			gotKey, ok := adminService.lastResourceActor.SubjectKey()
			require.True(t, ok)
			require.Equal(t, wantKey, gotKey)
			require.Positive(t, settingRepo.calls)
			require.Contains(t, settingRepo.values[service.SettingKeyDefaultSubscriptions], `"group_id":17`)
			require.Contains(t, settingRepo.values[service.SettingKeyAuthSourceDefaultEmailSubscriptions], `"group_id":19`)
		})
	}
}

func TestRedeemAndSettingResourceRoutesKeepActorGuardAtEntry(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	packageDir := filepath.Dir(currentFile)
	routesFile := filepath.Clean(filepath.Join(packageDir, "../../server/routes/admin.go"))

	wantedRoutes := map[string]map[string]bool{
		"h.Admin.Redeem": {
			"List":        false,
			"GetByID":     false,
			"Delete":      false,
			"BatchDelete": false,
			"Expire":      false,
			"Export":      false,
		},
		"h.Admin.Setting": {
			"GetSettings":    false,
			"UpdateSettings": false,
		},
	}
	parsedRoutes, err := parser.ParseFile(token.NewFileSet(), routesFile, nil, 0)
	require.NoError(t, err)
	ast.Inspect(parsedRoutes, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		chain := redeemSettingSelectorChain(call.Args[len(call.Args)-1])
		if len(chain) < 2 {
			return true
		}
		prefix := strings.Join(chain[:len(chain)-1], ".")
		if methods, wanted := wantedRoutes[prefix]; wanted {
			if _, wantedMethod := methods[chain[len(chain)-1]]; wantedMethod {
				methods[chain[len(chain)-1]] = true
			}
		}
		return true
	})
	for prefix, methods := range wantedRoutes {
		for method, found := range methods {
			require.Truef(t, found, "%s.%s is not registered in routes/admin.go", prefix, method)
		}
	}

	wantedHandlers := map[string]map[string]bool{
		"redeem_handler.go": {
			"List":        false,
			"GetByID":     false,
			"Delete":      false,
			"BatchDelete": false,
			"Expire":      false,
			"Export":      false,
		},
		"setting_handler.go": {
			"GetSettings": false,
		},
		"setting_handler_update.go": {
			"UpdateSettings": false,
		},
	}
	for filename, methods := range wantedHandlers {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(packageDir, filename), nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if _, wanted := methods[function.Name.Name]; !wanted {
				continue
			}
			methods[function.Name.Name] = true
			requireRedeemSettingActorGuardAtEntry(t, filename, function)
		}
		for method, found := range methods {
			require.Truef(t, found, "%s.%s handler missing", filename, method)
		}
	}
}

func requireRedeemSettingActorGuardAtEntry(t *testing.T, filename string, function *ast.FuncDecl) {
	t.Helper()
	require.GreaterOrEqual(t, len(function.Body.List), 2, "%s.%s must begin with the Actor guard", filename, function.Name.Name)
	assignment, ok := function.Body.List[0].(*ast.AssignStmt)
	require.True(t, ok, "%s.%s first statement must resolve Actor", filename, function.Name.Name)
	require.Len(t, assignment.Lhs, 2)
	require.Len(t, assignment.Rhs, 1)
	actorName, ok := assignment.Lhs[0].(*ast.Ident)
	require.True(t, ok)
	okName, ok := assignment.Lhs[1].(*ast.Ident)
	require.True(t, ok)
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	require.True(t, ok)
	resolver, ok := call.Fun.(*ast.Ident)
	require.True(t, ok)
	require.Equal(t, "adminResourceActor", resolver.Name)

	conditional, ok := function.Body.List[1].(*ast.IfStmt)
	require.True(t, ok, "%s.%s second statement must fail closed", filename, function.Name.Name)
	negation, ok := conditional.Cond.(*ast.UnaryExpr)
	require.True(t, ok)
	require.Equal(t, token.NOT, negation.Op)
	conditionName, ok := negation.X.(*ast.Ident)
	require.True(t, ok)
	require.Equal(t, okName.Name, conditionName.Name)
	require.True(t, redeemSettingBlockReturns(conditional.Body))

	actorFlows := false
	for _, statement := range function.Body.List[2:] {
		ast.Inspect(statement, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Name == actorName.Name {
				actorFlows = true
			}
			return true
		})
	}
	require.True(t, actorFlows, "%s.%s does not pass Actor beyond the guard", filename, function.Name.Name)
}

func redeemSettingBlockReturns(block *ast.BlockStmt) bool {
	if block == nil {
		return false
	}
	for _, statement := range block.List {
		if _, ok := statement.(*ast.ReturnStmt); ok {
			return true
		}
	}
	return false
}

func redeemSettingSelectorChain(expression ast.Expr) []string {
	switch value := expression.(type) {
	case *ast.Ident:
		return []string{value.Name}
	case *ast.SelectorExpr:
		return append(redeemSettingSelectorChain(value.X), value.Sel.Name)
	default:
		return nil
	}
}
