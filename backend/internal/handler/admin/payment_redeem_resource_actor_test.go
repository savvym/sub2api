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

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPaymentPlanAndRedeemHandlersRejectMissingActorBeforeRequestParsing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	paymentHandler := NewPaymentHandler(nil, &service.PaymentConfigService{})
	redeemHandler := NewRedeemHandler(newStubAdminService(), &service.RedeemService{})
	router := gin.New()
	router.GET("/payment/plans", paymentHandler.ListPlans)
	router.POST("/payment/plans", paymentHandler.CreatePlan)
	router.PUT("/payment/plans/:id", paymentHandler.UpdatePlan)
	router.DELETE("/payment/plans/:id", paymentHandler.DeletePlan)
	router.POST("/redeem/generate", redeemHandler.Generate)
	router.POST("/redeem/create-and-redeem", redeemHandler.CreateAndRedeem)
	router.POST("/redeem/batch-update", redeemHandler.BatchUpdate)

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/payment/plans"},
		{method: http.MethodPost, path: "/payment/plans", body: "{"},
		{method: http.MethodPut, path: "/payment/plans/not-an-id", body: "{"},
		{method: http.MethodDelete, path: "/payment/plans/not-an-id"},
		{method: http.MethodPost, path: "/redeem/generate", body: "{"},
		{method: http.MethodPost, path: "/redeem/create-and-redeem", body: "{"},
		{method: http.MethodPost, path: "/redeem/batch-update", body: "{"},
	}

	for _, request := range requests {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
			httpRequest.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, httpRequest)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
		})
	}
}

func TestPaymentPlanAndRedeemActorRouteCoverage(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	packageDir := filepath.Dir(currentFile)

	paymentMethods := registeredRouteMethodsForHandler(
		t,
		filepath.Clean(filepath.Join(packageDir, "../../server/routes/payment.go")),
		[]string{"adminPaymentHandler"},
	)
	redeemMethods := registeredRouteMethodsForHandler(
		t,
		filepath.Clean(filepath.Join(packageDir, "../../server/routes/admin.go")),
		[]string{"h", "Admin", "Redeem"},
	)
	guarded := guardedAdminHandlerMethods(t, packageDir)

	for _, method := range []string{"ListPlans", "CreatePlan", "UpdatePlan", "DeletePlan"} {
		require.Truef(t, paymentMethods[method], "PaymentHandler.%s is missing from routes/payment.go", method)
		require.Truef(t, guarded["PaymentHandler."+method], "PaymentHandler.%s is missing the Actor guard", method)
	}
	for _, method := range []string{"Generate", "CreateAndRedeem", "BatchUpdate"} {
		require.Truef(t, redeemMethods[method], "RedeemHandler.%s is missing from routes/admin.go", method)
		require.Truef(t, guarded["RedeemHandler."+method], "RedeemHandler.%s is missing the Actor guard", method)
	}
}

func registeredRouteMethodsForHandler(t *testing.T, filename string, handlerPrefix []string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	require.NoError(t, err)

	methods := make(map[string]bool)
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isRouteRegistrationMethod(selector.Sel.Name) {
			return true
		}
		chain := selectorChain(call.Args[len(call.Args)-1])
		if len(chain) != len(handlerPrefix)+1 {
			return true
		}
		for i := range handlerPrefix {
			if chain[i] != handlerPrefix[i] {
				return true
			}
		}
		methods[chain[len(chain)-1]] = true
		return true
	})
	return methods
}
