package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminResourceActorAcceptsTrustedUserAndServicePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		actor           authz.Actor
		compatibilityID int64
		wantKey         string
	}{
		{
			name:            "jwt user",
			actor:           adminHandlerTestActor(t, authz.SubjectKindUser, 42),
			compatibilityID: 42,
			wantKey:         "user:42",
		},
		{
			name:            "admin api key service principal",
			actor:           adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 42),
			compatibilityID: 1,
			wantKey:         "service_principal:42",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/?actor_user_id=999", strings.NewReader(`{"actor":{"user_id":999}}`))
			context.Request = context.Request.WithContext(authz.ContextWithActor(context.Request.Context(), testCase.actor))
			context.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: testCase.compatibilityID})

			got, ok := adminResourceActor(context)
			require.True(t, ok)
			key, keyOK := got.SubjectKey()
			require.True(t, keyOK)
			require.Equal(t, testCase.wantKey, key)
			require.False(t, recorder.Result().StatusCode >= http.StatusBadRequest)
		})
	}
}

func TestAdminResourceActorFailsClosedBeforeServiceUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userActor := adminHandlerTestActor(t, authz.SubjectKindUser, 42)
	tests := []struct {
		name    string
		actor   *authz.Actor
		subject middleware.AuthSubject
	}{
		{name: "missing actor", subject: middleware.AuthSubject{UserID: 42}},
		{name: "missing compatibility subject", actor: &userActor},
		{name: "mismatched user", actor: &userActor, subject: middleware.AuthSubject{UserID: 41}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if testCase.actor != nil {
				context.Request = context.Request.WithContext(authz.ContextWithActor(context.Request.Context(), *testCase.actor))
			}
			if testCase.subject.UserID > 0 {
				context.Set(string(middleware.ContextKeyUser), testCase.subject)
			}

			_, ok := adminResourceActor(context)
			require.False(t, ok)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
		})
	}
}

func TestRegisteredAdminAccountAndGroupHandlersGuardActor(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	packageDir := filepath.Dir(currentFile)
	routesFile := filepath.Clean(filepath.Join(packageDir, "../../server/routes/admin.go"))

	registered := registeredAdminResourceHandlers(t, routesFile)
	require.NotEmpty(t, registered)
	require.Contains(t, registered, "SubscriptionHandler.ListByGroup")
	require.Contains(t, registered, "ScheduledTestHandler.ListByAccount")
	require.Contains(t, registered, "ScheduledTestHandler.Create")
	require.Contains(t, registered, "ScheduledTestHandler.Update")
	require.Contains(t, registered, "ScheduledTestHandler.Delete")
	require.Contains(t, registered, "ScheduledTestHandler.ListResults")
	require.Contains(t, registered, "OpenAIOAuthHandler.GenerateAuthURL")
	require.Contains(t, registered, "OpenAIOAuthHandler.QueryQuota")
	require.Contains(t, registered, "GrokOAuthHandler.GenerateAuthURL")
	require.Contains(t, registered, "GrokOAuthHandler.QueryQuota")
	require.Contains(t, registered, "GeminiOAuthHandler.GenerateAuthURL")
	require.Contains(t, registered, "AntigravityOAuthHandler.RefreshToken")
	require.Contains(t, registered, "CNProviderHandler.QueryQuota")
	require.Contains(t, registered, "ProxyHandler.GetProxyAccounts")
	require.Contains(t, registered, "SubscriptionHandler.List")
	require.Contains(t, registered, "AdminAPIKeyHandler.UpdateGroup")
	require.Contains(t, registered, "UserHandler.List")
	require.Contains(t, registered, "UserHandler.GetByID")
	require.Contains(t, registered, "UserHandler.Create")
	require.Contains(t, registered, "UserHandler.Update")
	require.Contains(t, registered, "UserHandler.Delete")
	require.Contains(t, registered, "UserHandler.GetUserAPIKeys")
	require.Contains(t, registered, "UserHandler.ReplaceGroup")
	require.Contains(t, registered, "UserHandler.GetUserRPMStatus")
	require.Contains(t, registered, "ChannelHandler.List")
	require.Contains(t, registered, "ChannelHandler.GetByID")
	require.Contains(t, registered, "ChannelHandler.Create")
	require.Contains(t, registered, "ChannelHandler.Update")
	require.Contains(t, registered, "ChannelHandler.Delete")
	require.Contains(t, registered, "ChannelMonitorRequestTemplateHandler.List")
	require.Contains(t, registered, "ChannelMonitorRequestTemplateHandler.Apply")
	require.Contains(t, registered, "ContentModerationHandler.TestAPIKeys")
	require.Contains(t, registered, "RedeemHandler.Generate")
	require.Contains(t, registered, "RedeemHandler.CreateAndRedeem")
	require.Contains(t, registered, "RedeemHandler.BatchUpdate")
	require.NotContains(t, registered, "ChannelHandler.GetModelDefaultPricing")
	require.NotContains(t, registered, "ChannelHandler.SyncPricingModels")
	require.NotContains(t, registered, "DashboardHandler.GetGroupStats")
	require.NotContains(t, registered, "GeminiOAuthHandler.GetCapabilities")
	require.NotContains(t, registered, "GrokOAuthHandler.GetCapabilities")
	require.NotContains(t, registered, "GrokOAuthHandler.RuntimeSanity")
	require.NotContains(t, registered, "AccountHandler.GetAntigravityDefaultModelMapping")
	guarded := guardedAdminHandlerMethods(t, packageDir)

	for handler := range registered {
		require.Truef(t, guarded[handler], "%s is registered without a checked adminResourceActor flow", handler)
	}

	entryGuarded := entryGuardedAdminHandlerMethods(t, packageDir)
	for handler := range registered {
		require.Truef(t, entryGuarded[handler], "%s must fail closed on Actor before parsing request input", handler)
	}
}

func registeredAdminResourceHandlers(t *testing.T, filename string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	require.NoError(t, err)

	result := make(map[string]bool)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || !hasNamedParameter(function, "admin") {
			continue
		}
		groupPrefixes := map[string]string{"admin": ""}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
				identifier, identifierOK := assignment.Lhs[0].(*ast.Ident)
				call, callOK := assignment.Rhs[0].(*ast.CallExpr)
				if identifierOK && callOK {
					if prefix, prefixOK := registeredRouteGroupPrefix(call, groupPrefixes); prefixOK {
						groupPrefixes[identifier.Name] = prefix
					}
				}
			}

			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !isRouteRegistrationMethod(selector.Sel.Name) {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			prefix, ok := groupPrefixes[receiver.Name]
			if !ok {
				return true
			}
			route, ok := stringLiteral(call.Args[0])
			if !ok || !isAdminResourceRoute(selector.Sel.Name, joinRoutePath(prefix, route)) {
				return true
			}
			chain := selectorChain(call.Args[len(call.Args)-1])
			if len(chain) != 4 || chain[0] != "h" || chain[1] != "Admin" {
				return true
			}
			handlerType := chain[2] + "Handler"
			switch chain[2] {
			case "APIKey":
				handlerType = "AdminAPIKeyHandler"
			case "ChannelMonitorTemplate":
				handlerType = "ChannelMonitorRequestTemplateHandler"
			}
			result[handlerType+"."+chain[3]] = true
			return true
		})
	}
	return result
}

func hasNamedParameter(function *ast.FuncDecl, name string) bool {
	if function.Type.Params == nil {
		return false
	}
	for _, field := range function.Type.Params.List {
		for _, parameter := range field.Names {
			if parameter.Name == name {
				return true
			}
		}
	}
	return false
}

func registeredRouteGroupPrefix(call *ast.CallExpr, groupPrefixes map[string]string) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Group" || len(call.Args) != 1 {
		return "", false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	prefix, ok := groupPrefixes[receiver.Name]
	if !ok {
		return "", false
	}
	path, ok := stringLiteral(call.Args[0])
	if !ok {
		return "", false
	}
	return joinRoutePath(prefix, path), true
}

func isRouteRegistrationMethod(name string) bool {
	switch name {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "Any", "Handle":
		return true
	default:
		return false
	}
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func joinRoutePath(prefix, path string) string {
	if prefix == "" {
		if path == "" {
			return "/"
		}
		return "/" + strings.TrimLeft(path, "/")
	}
	if path == "" {
		return "/" + strings.Trim(prefix, "/")
	}
	return "/" + strings.Trim(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

func isAdminResourceRoute(method, path string) bool {
	if path == "/accounts/antigravity/default-model-mapping" {
		return false
	}
	if path == "/accounts" || strings.HasPrefix(path, "/accounts/") ||
		path == "/groups" || strings.HasPrefix(path, "/groups/") ||
		path == "/scheduled-test-plans" || strings.HasPrefix(path, "/scheduled-test-plans/") ||
		path == "/subscriptions" || strings.HasPrefix(path, "/subscriptions/") ||
		(strings.HasPrefix(path, "/users/") && strings.HasSuffix(path, "/subscriptions")) ||
		strings.HasPrefix(path, "/openai/") ||
		strings.HasPrefix(path, "/antigravity/oauth/") ||
		strings.HasPrefix(path, "/cn-providers/accounts/") ||
		(strings.HasPrefix(path, "/proxies/") && strings.HasSuffix(path, "/accounts")) {
		return true
	}
	if path == "/api-keys/:id" {
		return method == "PUT"
	}
	if path == "/users" {
		return method == "GET" || method == "POST"
	}
	if path == "/users/:id" {
		return method == "GET" || method == "PUT" || method == "DELETE"
	}
	if path == "/users/:id/api-keys" || path == "/users/:id/rpm-status" {
		return method == "GET"
	}
	if path == "/users/:id/replace-group" {
		return method == "POST"
	}
	if path == "/channels" {
		return method == "GET" || method == "POST"
	}
	if path == "/channels/:id" {
		return method == "GET" || method == "PUT" || method == "DELETE"
	}
	if path == "/channel-monitor-templates" || strings.HasPrefix(path, "/channel-monitor-templates/") {
		return true
	}
	if path == "/risk-control/api-keys/test" {
		return method == "POST"
	}
	if path == "/redeem-codes/generate" || path == "/redeem-codes/create-and-redeem" || path == "/redeem-codes/batch-update" {
		return method == "POST"
	}
	if strings.HasPrefix(path, "/gemini/oauth/") {
		return path != "/gemini/oauth/capabilities"
	}
	if strings.HasPrefix(path, "/grok/") {
		return path != "/grok/oauth/capabilities" && path != "/grok/runtime-sanity"
	}
	return false
}

func guardedAdminHandlerMethods(t *testing.T, packageDir string) map[string]bool {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), packageDir, func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	result := make(map[string]bool)
	for _, parsedPackage := range packages {
		for _, file := range parsedPackage.Files {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv == nil || function.Body == nil || len(function.Recv.List) != 1 {
					continue
				}
				receiverType := receiverTypeName(function.Recv.List[0].Type)
				if receiverType == "" {
					continue
				}
				if _, guardEnd, guarded := checkedAdminResourceActorGuard(function); guarded && actorFlowsAfterGuard(function, guardEnd) {
					result[receiverType+"."+function.Name.Name] = true
				}
			}
		}
	}
	return result
}

func entryGuardedAdminHandlerMethods(t *testing.T, packageDir string) map[string]bool {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), packageDir, func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	result := make(map[string]bool)
	for _, parsedPackage := range packages {
		for _, file := range parsedPackage.Files {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv == nil || function.Body == nil || len(function.Recv.List) != 1 {
					continue
				}
				receiverType := receiverTypeName(function.Recv.List[0].Type)
				_, guardEnd, guarded := checkedAdminResourceActorGuard(function)
				if guarded && guardStartsAtEntry(function, guardEnd) && actorFlowsAfterGuard(function, guardEnd) {
					result[receiverType+"."+function.Name.Name] = true
				}
			}
		}
	}
	return result
}

func checkedAdminResourceActorGuard(function *ast.FuncDecl) (string, token.Pos, bool) {
	for index, statement := range function.Body.List {
		if conditional, ok := statement.(*ast.IfStmt); ok && conditional.Init != nil {
			actorName, okName, ok := adminResourceActorAssignment(conditional.Init)
			if ok && isNegatedIdentifier(conditional.Cond, okName) && blockReturns(conditional.Body) {
				return actorName, conditional.End(), true
			}
		}

		actorName, okName, ok := adminResourceActorAssignment(statement)
		if !ok || index+1 >= len(function.Body.List) {
			continue
		}
		conditional, ok := function.Body.List[index+1].(*ast.IfStmt)
		if ok && conditional.Init == nil && isNegatedIdentifier(conditional.Cond, okName) && blockReturns(conditional.Body) {
			return actorName, conditional.End(), true
		}
	}
	return "", token.NoPos, false
}

func adminResourceActorAssignment(statement ast.Stmt) (string, string, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
		return "", "", false
	}
	actorName, actorOK := assignment.Lhs[0].(*ast.Ident)
	okName, okOK := assignment.Lhs[1].(*ast.Ident)
	call, callOK := assignment.Rhs[0].(*ast.CallExpr)
	if !actorOK || !okOK || !callOK {
		return "", "", false
	}
	guard, guardOK := call.Fun.(*ast.Ident)
	if !guardOK || guard.Name != "adminResourceActor" {
		return "", "", false
	}
	return actorName.Name, okName.Name, true
}

func isNegatedIdentifier(expression ast.Expr, name string) bool {
	unary, ok := expression.(*ast.UnaryExpr)
	if !ok || unary.Op != token.NOT {
		return false
	}
	identifier, ok := unary.X.(*ast.Ident)
	return ok && identifier.Name == name
}

func blockReturns(block *ast.BlockStmt) bool {
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

func actorFlowsAfterGuard(function *ast.FuncDecl, guardEnd token.Pos) bool {
	actorName, _, guarded := checkedAdminResourceActorGuard(function)
	if !guarded {
		return false
	}
	if actorName == "_" {
		return true
	}
	flows := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || call.Pos() <= guardEnd {
			return true
		}
		for _, argument := range call.Args {
			identifier, ok := argument.(*ast.Ident)
			if ok && identifier.Name == actorName {
				flows = true
				return false
			}
		}
		return true
	})
	return flows
}

func guardStartsAtEntry(function *ast.FuncDecl, guardEnd token.Pos) bool {
	if len(function.Body.List) == 0 {
		return false
	}
	first := function.Body.List[0]
	if conditional, ok := first.(*ast.IfStmt); ok && conditional.Init != nil {
		return conditional.End() == guardEnd
	}
	if len(function.Body.List) < 2 {
		return false
	}
	conditional, ok := function.Body.List[1].(*ast.IfStmt)
	return ok && conditional.End() == guardEnd
}

func selectorChain(expression ast.Expr) []string {
	switch value := expression.(type) {
	case *ast.Ident:
		return []string{value.Name}
	case *ast.SelectorExpr:
		return append(selectorChain(value.X), value.Sel.Name)
	default:
		return nil
	}
}

func receiverTypeName(expression ast.Expr) string {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}
