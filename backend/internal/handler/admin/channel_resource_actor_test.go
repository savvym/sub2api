package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelActorGuardRequest struct {
	method string
	path   string
	body   string
}

var channelActorGuardRequests = []channelActorGuardRequest{
	{method: http.MethodGet, path: "/channels"},
	{method: http.MethodGet, path: "/channels/not-an-id"},
	{method: http.MethodPost, path: "/channels", body: `{`},
	{method: http.MethodPut, path: "/channels/not-an-id", body: `{`},
	{method: http.MethodDelete, path: "/channels/not-an-id"},
}

func TestChannelAdminCRUDHandlersFailClosedWithoutActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	handler := NewChannelHandler(nil, nil, nil)
	router.GET("/channels", handler.List)
	router.GET("/channels/:id", handler.GetByID)
	router.POST("/channels", handler.Create)
	router.PUT("/channels/:id", handler.Update)
	router.DELETE("/channels/:id", handler.Delete)

	for _, request := range channelActorGuardRequests {
		recorder := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		if request.body != "" {
			httpRequest.Header.Set("Content-Type", "application/json")
		}

		router.ServeHTTP(recorder, httpRequest)

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
	}
}

func TestChannelAdminCRUDHandlersGuardAndPropagateActor(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	filename := filepath.Join(filepath.Dir(currentFile), "channel_handler.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	require.NoError(t, err)

	expectedFacades := map[string]string{
		"List":    "AdminListChannels",
		"GetByID": "AdminGetChannel",
		"Create":  "AdminCreateChannel",
		"Update":  "AdminUpdateChannel",
		"Delete":  "AdminDeleteChannel",
	}
	checked := make(map[string]bool, len(expectedFacades))

	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		facade, required := expectedFacades[function.Name.Name]
		if !required || !channelActorReceiverIs(function, "ChannelHandler") {
			continue
		}

		actorName, guardPosition, guardOK := channelActorFirstStatementGuard(function)
		require.Truef(t, guardOK, "%s must obtain adminResourceActor in its first statement", function.Name.Name)

		facadeCall, facadePosition := channelActorFindFacadeCall(function, facade)
		require.NotNilf(t, facadeCall, "%s must call %s", function.Name.Name, facade)
		require.Less(t, guardPosition, facadePosition, "%s must guard before calling %s", function.Name.Name, facade)
		require.Truef(t, channelActorCallUsesIdentifier(facadeCall, actorName), "%s must pass the guarded actor to %s", function.Name.Name, facade)
		checked[function.Name.Name] = true
	}

	for method := range expectedFacades {
		require.Truef(t, checked[method], "ChannelHandler.%s was not checked", method)
	}
}

func channelActorReceiverIs(function *ast.FuncDecl, name string) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	receiver := function.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)
	return ok && identifier.Name == name
}

func channelActorFirstStatementGuard(function *ast.FuncDecl) (string, token.Pos, bool) {
	if len(function.Body.List) == 0 {
		return "", token.NoPos, false
	}
	assignment, ok := function.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) < 1 || len(assignment.Rhs) != 1 {
		return "", token.NoPos, false
	}
	actor, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return "", token.NoPos, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", token.NoPos, false
	}
	guard, ok := call.Fun.(*ast.Ident)
	if !ok || guard.Name != "adminResourceActor" {
		return "", token.NoPos, false
	}
	return actor.Name, call.Pos(), true
}

func channelActorFindFacadeCall(function *ast.FuncDecl, facade string) (*ast.CallExpr, token.Pos) {
	var found *ast.CallExpr
	position := token.NoPos
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == facade {
			found = call
			position = call.Pos()
			return false
		}
		return true
	})
	return found, position
}

func channelActorCallUsesIdentifier(call *ast.CallExpr, name string) bool {
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}
	return false
}
