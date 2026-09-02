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
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type directResourceHandlerRequest struct {
	method string
	path   string
	body   string
}

var directResourceHandlerGuardRequests = []directResourceHandlerRequest{
	{method: http.MethodGet, path: "/risk/config"},
	{method: http.MethodPut, path: "/risk/config", body: `{`},
	{method: http.MethodPost, path: "/risk/api-keys/test", body: `{`},
	{method: http.MethodGet, path: "/monitors?page=bad"},
	{method: http.MethodGet, path: "/monitors/not-an-id"},
	{method: http.MethodPost, path: "/monitors", body: `{`},
	{method: http.MethodPost, path: "/monitors/not-an-id/duplicate"},
	{method: http.MethodPut, path: "/monitors/not-an-id", body: `{`},
	{method: http.MethodDelete, path: "/monitors/not-an-id"},
	{method: http.MethodGet, path: "/ops/rules"},
	{method: http.MethodPost, path: "/ops/rules", body: `{`},
	{method: http.MethodPut, path: "/ops/rules/not-an-id", body: `{`},
	{method: http.MethodDelete, path: "/ops/rules/not-an-id"},
	{method: http.MethodPost, path: "/ops/silences", body: `{`},
	{method: http.MethodGet, path: "/templates"},
	{method: http.MethodGet, path: "/templates/not-an-id"},
	{method: http.MethodPost, path: "/templates", body: `{`},
	{method: http.MethodPut, path: "/templates/not-an-id", body: `{`},
	{method: http.MethodDelete, path: "/templates/not-an-id"},
	{method: http.MethodGet, path: "/templates/not-an-id/monitors"},
	{method: http.MethodPost, path: "/templates/not-an-id/apply", body: `{`},
}

type directResourceTemplateRepo struct {
	service.ChannelMonitorRequestTemplateRepository
}

func (directResourceTemplateRepo) List(context.Context, service.ChannelMonitorRequestTemplateListParams) ([]*service.ChannelMonitorRequestTemplate, error) {
	return []*service.ChannelMonitorRequestTemplate{}, nil
}

func setupDirectResourceHandlerRouter(actor *authz.Actor, compatibilityUserID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != nil {
			c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), *actor))
		}
		if compatibilityUserID > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: compatibilityUserID})
		}
		c.Next()
	})

	content := NewContentModerationHandler(nil)
	monitors := NewChannelMonitorHandler(nil)
	ops := NewOpsHandler(nil)
	templates := NewChannelMonitorRequestTemplateHandler(service.NewChannelMonitorRequestTemplateService(directResourceTemplateRepo{}))
	router.GET("/risk/config", content.GetConfig)
	router.PUT("/risk/config", content.UpdateConfig)
	router.POST("/risk/api-keys/test", content.TestAPIKeys)
	router.GET("/monitors", monitors.List)
	router.GET("/monitors/:id", monitors.Get)
	router.POST("/monitors", monitors.Create)
	router.POST("/monitors/:id/duplicate", monitors.Duplicate)
	router.PUT("/monitors/:id", monitors.Update)
	router.DELETE("/monitors/:id", monitors.Delete)
	router.GET("/ops/rules", ops.ListAlertRules)
	router.POST("/ops/rules", ops.CreateAlertRule)
	router.PUT("/ops/rules/:id", ops.UpdateAlertRule)
	router.DELETE("/ops/rules/:id", ops.DeleteAlertRule)
	router.POST("/ops/silences", ops.CreateAlertSilence)
	router.GET("/templates", templates.List)
	router.GET("/templates/:id", templates.Get)
	router.POST("/templates", templates.Create)
	router.PUT("/templates/:id", templates.Update)
	router.DELETE("/templates/:id", templates.Delete)
	router.GET("/templates/:id/monitors", templates.AssociatedMonitors)
	router.POST("/templates/:id/apply", templates.Apply)
	return router
}

func performDirectResourceHandlerRequest(router *gin.Engine, request directResourceHandlerRequest) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
	if request.body != "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestDirectResourceHandlersRejectUnavailableActorBeforeInputAndDependencies(t *testing.T) {
	mismatched := adminHandlerTestActor(t, authz.SubjectKindUser, 41)
	for _, testCase := range []struct {
		name                string
		actor               *authz.Actor
		compatibilityUserID int64
	}{
		{name: "missing", compatibilityUserID: 1},
		{name: "mismatched jwt subject", actor: &mismatched, compatibilityUserID: 42},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := setupDirectResourceHandlerRouter(testCase.actor, testCase.compatibilityUserID)
			for _, request := range directResourceHandlerGuardRequests {
				recorder := performDirectResourceHandlerRequest(router, request)
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code,
					"%s %s: %s", request.method, request.path, recorder.Body.String())
				require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
			}
		})
	}
}

func TestDirectResourceHandlersAcceptTrustedActorKinds(t *testing.T) {
	requests := []directResourceHandlerRequest{
		{method: http.MethodPut, path: "/risk/config", body: `{`},
		{method: http.MethodPost, path: "/risk/api-keys/test", body: `{`},
		{method: http.MethodPost, path: "/monitors", body: `{`},
		{method: http.MethodPost, path: "/monitors/not-an-id/duplicate"},
		{method: http.MethodPut, path: "/monitors/not-an-id", body: `{`},
		{method: http.MethodDelete, path: "/monitors/not-an-id"},
		{method: http.MethodGet, path: "/ops/rules"},
		{method: http.MethodPost, path: "/ops/rules", body: `{`},
		{method: http.MethodPut, path: "/ops/rules/not-an-id", body: `{`},
		{method: http.MethodDelete, path: "/ops/rules/not-an-id"},
		{method: http.MethodPost, path: "/ops/silences", body: `{`},
		{method: http.MethodGet, path: "/templates"},
		{method: http.MethodGet, path: "/templates/not-an-id"},
		{method: http.MethodPost, path: "/templates", body: `{`},
		{method: http.MethodPut, path: "/templates/not-an-id", body: `{`},
		{method: http.MethodDelete, path: "/templates/not-an-id"},
		{method: http.MethodGet, path: "/templates/not-an-id/monitors"},
		{method: http.MethodPost, path: "/templates/not-an-id/apply", body: `{`},
	}
	for _, testCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{name: "jwt user", actor: adminHandlerTestActor(t, authz.SubjectKindUser, 41), compatibilityUserID: 41},
		{name: "admin api key service principal", actor: adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73), compatibilityUserID: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := setupDirectResourceHandlerRouter(&testCase.actor, testCase.compatibilityUserID)
			for _, request := range requests {
				recorder := performDirectResourceHandlerRequest(router, request)
				require.NotContains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`,
					"%s %s: %s", request.method, request.path, recorder.Body.String())
			}
		})
	}
}

type directResourceHandlerSpec struct {
	file     string
	receiver string
	method   string
	facades  []string
}

func TestDirectResourceHandlersGuardAtEntryAndPropagateActor(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	directory := filepath.Dir(currentFile)
	specs := []directResourceHandlerSpec{
		{file: "content_moderation_handler.go", receiver: "ContentModerationHandler", method: "GetConfig", facades: []string{"AdminGetContentModerationConfig"}},
		{file: "content_moderation_handler.go", receiver: "ContentModerationHandler", method: "UpdateConfig", facades: []string{"AdminUpdateContentModerationConfig"}},
		{file: "content_moderation_handler.go", receiver: "ContentModerationHandler", method: "TestAPIKeys", facades: []string{"AdminTestContentModerationAPIKeys"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "List", facades: []string{"AdminListChannelMonitorRequestTemplates"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "Get", facades: []string{"AdminGetChannelMonitorRequestTemplate"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "Create", facades: []string{"AdminCreateChannelMonitorRequestTemplate"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "Update", facades: []string{"AdminUpdateChannelMonitorRequestTemplate"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "Delete", facades: []string{"AdminDeleteChannelMonitorRequestTemplate"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "AssociatedMonitors", facades: []string{"AdminListAssociatedChannelMonitors"}},
		{file: "channel_monitor_template_handler.go", receiver: "ChannelMonitorRequestTemplateHandler", method: "Apply", facades: []string{"AdminApplyChannelMonitorRequestTemplate"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "List", facades: []string{"AdminListChannelMonitors"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "Get", facades: []string{"AdminGetChannelMonitor"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "Create", facades: []string{"AdminCreateChannelMonitor"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "Duplicate", facades: []string{"AdminDuplicateChannelMonitor", "AdminRecoverDuplicateChannelMonitor"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "Update", facades: []string{"AdminUpdateChannelMonitor"}},
		{file: "channel_monitor_handler.go", receiver: "ChannelMonitorHandler", method: "Delete", facades: []string{"AdminDeleteChannelMonitor"}},
		{file: "ops_alerts_handler.go", receiver: "OpsHandler", method: "ListAlertRules", facades: []string{"AdminListAlertRules"}},
		{file: "ops_alerts_handler.go", receiver: "OpsHandler", method: "CreateAlertRule", facades: []string{"AdminCreateAlertRule"}},
		{file: "ops_alerts_handler.go", receiver: "OpsHandler", method: "UpdateAlertRule", facades: []string{"AdminUpdateAlertRule"}},
		{file: "ops_alerts_handler.go", receiver: "OpsHandler", method: "DeleteAlertRule", facades: []string{"AdminDeleteAlertRule"}},
		{file: "ops_alerts_handler.go", receiver: "OpsHandler", method: "CreateAlertSilence", facades: []string{"AdminCreateAlertSilence"}},
	}

	parsedFiles := make(map[string]*ast.File)
	for _, spec := range specs {
		parsed := parsedFiles[spec.file]
		if parsed == nil {
			var err error
			parsed, err = parser.ParseFile(token.NewFileSet(), filepath.Join(directory, spec.file), nil, 0)
			require.NoError(t, err)
			parsedFiles[spec.file] = parsed
		}
		function := directResourceFindHandler(parsed, spec.receiver, spec.method)
		require.NotNilf(t, function, "%s.%s not found", spec.receiver, spec.method)
		actorName, guarded := directResourceEntryActorGuard(function)
		require.Truef(t, guarded, "%s.%s must call adminResourceActor in its first statement", spec.receiver, spec.method)
		for _, facade := range spec.facades {
			call := directResourceFindFacadeCall(function, facade)
			require.NotNilf(t, call, "%s.%s must call %s", spec.receiver, spec.method, facade)
			require.Truef(t, directResourceCallUsesIdentifier(call, actorName),
				"%s.%s must pass its guarded Actor to %s", spec.receiver, spec.method, facade)
		}
	}

	templateFile := parsedFiles["channel_monitor_template_handler.go"]
	templateResponse := directResourceFindHandler(templateFile, "ChannelMonitorRequestTemplateHandler", "toResponse")
	require.NotNil(t, templateResponse)
	countCall := directResourceFindFacadeCall(templateResponse, "AdminCountAssociatedChannelMonitors")
	require.NotNil(t, countCall)
	require.True(t, directResourceCallUsesIdentifier(countCall, "actor"))
}

func directResourceFindHandler(file *ast.File, receiver, method string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != method || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		receiverExpression := function.Recv.List[0].Type
		if pointer, ok := receiverExpression.(*ast.StarExpr); ok {
			receiverExpression = pointer.X
		}
		identifier, ok := receiverExpression.(*ast.Ident)
		if ok && identifier.Name == receiver {
			return function
		}
	}
	return nil
}

func directResourceEntryActorGuard(function *ast.FuncDecl) (string, bool) {
	if function == nil || function.Body == nil || len(function.Body.List) < 2 {
		return "", false
	}
	assignment, ok := function.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
		return "", false
	}
	actorName, actorOK := assignment.Lhs[0].(*ast.Ident)
	okName, okOK := assignment.Lhs[1].(*ast.Ident)
	call, callOK := assignment.Rhs[0].(*ast.CallExpr)
	if !actorOK || !okOK || !callOK {
		return "", false
	}
	guard, guardOK := call.Fun.(*ast.Ident)
	if !guardOK || guard.Name != "adminResourceActor" {
		return "", false
	}
	conditional, ok := function.Body.List[1].(*ast.IfStmt)
	if !ok || conditional.Init != nil || !directResourceNegatesIdentifier(conditional.Cond, okName.Name) || !directResourceBlockReturns(conditional.Body) {
		return "", false
	}
	return actorName.Name, true
}

func directResourceNegatesIdentifier(expression ast.Expr, name string) bool {
	unary, ok := expression.(*ast.UnaryExpr)
	if !ok || unary.Op != token.NOT {
		return false
	}
	identifier, ok := unary.X.(*ast.Ident)
	return ok && identifier.Name == name
}

func directResourceBlockReturns(block *ast.BlockStmt) bool {
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

func directResourceFindFacadeCall(function *ast.FuncDecl, facade string) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == facade {
			found = call
			return false
		}
		return true
	})
	return found
}

func directResourceCallUsesIdentifier(call *ast.CallExpr, name string) bool {
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}
	return false
}

func TestDirectResourceAdminRoutesStayRegisteredToGuardedHandlers(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	routesFile := filepath.Join(filepath.Dir(currentFile), "..", "..", "server", "routes", "admin.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), routesFile, nil, 0)
	require.NoError(t, err)

	expected := map[string][]string{
		"registerContentModerationRoutes": {
			"GET /config h.Admin.ContentModeration.GetConfig",
			"PUT /config h.Admin.ContentModeration.UpdateConfig",
		},
		"registerChannelMonitorRoutes": {
			"GET  h.Admin.ChannelMonitor.List",
			"POST  h.Admin.ChannelMonitor.Create",
			"GET /:id h.Admin.ChannelMonitor.Get",
			"POST /:id/duplicate h.Admin.ChannelMonitor.Duplicate",
			"PUT /:id h.Admin.ChannelMonitor.Update",
			"DELETE /:id h.Admin.ChannelMonitor.Delete",
		},
		"registerOpsRoutes": {
			"GET /alert-rules h.Admin.Ops.ListAlertRules",
			"POST /alert-rules h.Admin.Ops.CreateAlertRule",
			"PUT /alert-rules/:id h.Admin.Ops.UpdateAlertRule",
			"DELETE /alert-rules/:id h.Admin.Ops.DeleteAlertRule",
			"POST /alert-silences h.Admin.Ops.CreateAlertSilence",
		},
	}

	for functionName, routes := range expected {
		function := directResourceFindFunction(parsed, functionName)
		require.NotNilf(t, function, "%s not found", functionName)
		registered := directResourceRegisteredRoutes(function)
		for _, route := range routes {
			require.Containsf(t, registered, route, "%s must register %s", functionName, route)
		}
	}
}

func directResourceFindFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func directResourceRegisteredRoutes(function *ast.FuncDecl) map[string]struct{} {
	registered := make(map[string]struct{})
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "GET", "POST", "PUT", "DELETE":
		default:
			return true
		}
		pathLiteral, ok := call.Args[0].(*ast.BasicLit)
		if !ok || pathLiteral.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(pathLiteral.Value)
		if err != nil {
			return true
		}
		handler := directResourceSelectorName(call.Args[1])
		if handler != "" {
			registered[selector.Sel.Name+" "+path+" "+handler] = struct{}{}
		}
		return true
	})
	return registered
}

func directResourceSelectorName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := directResourceSelectorName(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	default:
		return ""
	}
}
