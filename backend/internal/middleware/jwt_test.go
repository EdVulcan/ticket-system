package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/config"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTAuthAcceptsHS256AndRejectsOtherAlgorithms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	config.GlobalConfig.Security.JWTSecret = "01234567890123456789012345678901"
	claims := service.Claims{
		UserID: 7, Username: "admin", Role: "admin", TenantID: 9,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}

	requestStatus := func(method jwt.SigningMethod) int {
		token := jwt.NewWithClaims(method, claims)
		signed, err := token.SignedString([]byte(config.GlobalConfig.Security.JWTSecret))
		if err != nil {
			t.Fatal(err)
		}
		engine := gin.New()
		engine.GET("/protected", JWTAuth(), func(ctx *gin.Context) {
			if ctx.GetUint("tenant_id") != 9 || ctx.GetString("role") != "admin" {
				t.Error("claims were not copied into request context")
			}
			ctx.Status(http.StatusNoContent)
		})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer "+signed)
		engine.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if status := requestStatus(jwt.SigningMethodHS256); status != http.StatusNoContent {
		t.Fatalf("HS256 status = %d, want 204", status)
	}
	if status := requestStatus(jwt.SigningMethodHS512); status != http.StatusUnauthorized {
		t.Fatalf("HS512 status = %d, want 401", status)
	}
}

func TestRequireAnyRoleSupportsStaffRoleLists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/allowed", func(ctx *gin.Context) { ctx.Set("role", "seller,checker") }, RequireAnyRole("checker"), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	engine.GET("/denied", func(ctx *gin.Context) { ctx.Set("role", "seller") }, RequireAnyRole("admin"), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})

	allowed := httptest.NewRecorder()
	engine.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/allowed", nil))
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed role status = %d", allowed.Code)
	}
	denied := httptest.NewRecorder()
	engine.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/denied", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied role status = %d", denied.Code)
	}
}

func TestRequirePlatformScopeRejectsTenantTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/platform", func(ctx *gin.Context) { ctx.Set("scope", "tenant") }, RequirePlatformScope(), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	denied := httptest.NewRecorder()
	engine.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/platform", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("tenant scope status = %d, want 403", denied.Code)
	}

	engine = gin.New()
	engine.GET("/platform", func(ctx *gin.Context) { ctx.Set("scope", "platform") }, RequirePlatformScope(), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	allowed := httptest.NewRecorder()
	engine.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/platform", nil))
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("platform scope status = %d, want 204", allowed.Code)
	}
}

func TestRequireTenantPermissionUsesFixedRoleMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/allowed", func(ctx *gin.Context) { ctx.Set("scope", "tenant"); ctx.Set("role", "team_operator") }, RequireTenantPermission("teams.write"), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	engine.GET("/denied", func(ctx *gin.Context) { ctx.Set("scope", "tenant"); ctx.Set("role", "viewer") }, RequireTenantPermission("teams.write"), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })

	allowed := httptest.NewRecorder()
	engine.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/allowed", nil))
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("team operator status=%d", allowed.Code)
	}
	denied := httptest.NewRecorder()
	engine.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/denied", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer write status=%d", denied.Code)
	}
}

func TestRequireTeamContractPermissionSeparatesReceptionFromCommercialTerms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	setRole := func(role string) gin.HandlerFunc {
		return func(ctx *gin.Context) {
			ctx.Set("scope", "tenant")
			ctx.Set("role", role)
		}
	}
	permission := RequireTenantPermission(authz.PermissionTeamContractsWrite)
	engine.POST("/team-operator", setRole(authz.RoleTeamOperator), permission, func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	engine.POST("/settlement-operator", setRole(authz.RoleSettlementOperator), permission, func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	engine.POST("/admin", setRole(authz.RoleTenantAdmin), permission, func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })

	tests := []struct {
		path string
		want int
	}{
		{"/team-operator", http.StatusForbidden},
		{"/settlement-operator", http.StatusNoContent},
		{"/admin", http.StatusNoContent},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, nil))
		if response.Code != test.want {
			t.Fatalf("POST %s status=%d, want %d", test.path, response.Code, test.want)
		}
	}
}
