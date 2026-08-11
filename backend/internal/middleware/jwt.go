package middleware

import (
	"net/http"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
			c.Abort()
			return
		}

		tokenString := parts[1]
		claims := &service.Claims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(config.GlobalConfig.Security.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}
		// JWT signature validity is not enough: users can be deleted, staff can
		// be frozen, and the platform can freeze a tenant while a token is still
		// within its nominal lifetime. Re-check the current ownership state on
		// every protected request. The nil guard only supports isolated middleware
		// tests before the application database is initialized.
		if model.DB != nil {
			if !sessionIsActive(claims) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "account or tenant is inactive"})
				c.Abort()
				return
			}
		}

		// Set context variables
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Set("tenant_id", claims.TenantID)
		c.Set("scope", claims.Scope)
		c.Set("platform_user_id", claims.PlatformUserID)
		c.Set("subject", claims.Subject)

		c.Next()
	}
}

func sessionIsActive(claims *service.Claims) bool {
	if claims.Scope == "platform" {
		var user model.PlatformUser
		return claims.TokenVersion > 0 && model.DB.Select("id", "status", "role", "token_version").Where("id = ?", claims.PlatformUserID).First(&user).Error == nil && user.Status == "active" && user.Role == claims.Role && user.TokenVersion == claims.TokenVersion
	}
	var tenant model.Tenant
	if err := model.DB.Select("id", "status").First(&tenant, claims.TenantID).Error; err != nil {
		return false
	}
	if tenant.Status != "" && tenant.Status != "active" {
		return false
	}
	if strings.HasPrefix(claims.Subject, "staff:") {
		var staff model.Staff
		return claims.TokenVersion > 0 && model.DB.Select("id", "tenant_id", "status", "roles", "token_version").Where("id = ? AND tenant_id = ?", claims.UserID, claims.TenantID).First(&staff).Error == nil && staff.Status == "active" && strings.TrimSpace(staff.Roles) == strings.TrimSpace(claims.Role) && staff.TokenVersion == claims.TokenVersion
	}
	var user model.User
	return claims.TokenVersion > 0 && model.DB.Select("id", "tenant_id", "role", "token_version").Where("id = ? AND tenant_id = ?", claims.UserID, claims.TenantID).First(&user).Error == nil && user.Role == claims.Role && user.TokenVersion == claims.TokenVersion
}

func RequirePlatformScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("scope") != "platform" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "platform scope required"})
			return
		}
		c.Next()
	}
}

func RequireAnyRole(allowed ...string) gin.HandlerFunc {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, role := range allowed {
		allowedSet[role] = struct{}{}
	}

	return func(c *gin.Context) {
		for _, role := range strings.Split(c.GetString("role"), ",") {
			if _, ok := allowedSet[strings.TrimSpace(role)]; ok {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission denied"})
	}
}

func RequireTenantPermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("scope") != "tenant" || !authz.HasTenantPermission(c.GetString("role"), permission) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前账号没有执行此操作的权限"})
			return
		}
		c.Next()
	}
}

func RequireAnyTenantCapability(allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetUint("tenant_id")
		if c.GetString("scope") != "tenant" || tenantID == 0 || len(allowed) == 0 || model.DB == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此业务能力"})
			return
		}
		var count int64
		err := model.DB.Model(&model.TenantCapability{}).
			Where("tenant_id = ? AND capability IN ? AND status = ?", tenantID, allowed, "active").
			Where("expires_at IS NULL OR expires_at > ?", time.Now()).
			Count(&count).Error
		if err != nil || count == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此业务能力"})
			return
		}
		c.Next()
	}
}

// RequireAnySupplierBusinessType narrows supplier-only operational routes to
// the fulfillment vertical they implement. Market role and business vertical
// are both required; a hotel supplier must not gain scenic gate operations.
func RequireAnySupplierBusinessType(allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetUint("tenant_id")
		if c.GetString("scope") != "tenant" || tenantID == 0 || len(allowed) == 0 || model.DB == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此供应业态"})
			return
		}
		now := time.Now()
		var count int64
		err := model.DB.Table("tenant_capabilities AS capability").
			Joins("JOIN tenants AS tenant ON tenant.id = capability.tenant_id AND tenant.deleted_at IS NULL").
			Joins("JOIN supplier_business_types AS business ON business.tenant_id = capability.tenant_id AND business.deleted_at IS NULL").
			Where("capability.tenant_id = ? AND capability.capability = ? AND capability.status = ?", tenantID, "supplier", "active").
			Where("capability.deleted_at IS NULL AND (capability.expires_at IS NULL OR capability.expires_at > ?)", now).
			Where("tenant.status = ?", "active").
			Where("business.business_type IN ? AND business.status = ?", allowed, "active").
			Count(&count).Error
		if err != nil || count == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此供应业态"})
			return
		}
		c.Next()
	}
}

// RequireConfiguredSupplierBusinessType keeps read-only historical supplier
// responsibilities available after a business vertical is suspended. The
// supplier identity may itself be suspended or expired because financial and
// verification history must remain available for operational close-out.
func RequireConfiguredSupplierBusinessType(allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetUint("tenant_id")
		if c.GetString("scope") != "tenant" || tenantID == 0 || len(allowed) == 0 || model.DB == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此供应业态的历史查询权限"})
			return
		}

		var count int64
		err := model.DB.Table("tenant_capabilities AS capability").
			Joins("JOIN tenants AS tenant ON tenant.id = capability.tenant_id AND tenant.deleted_at IS NULL").
			Joins("JOIN supplier_business_types AS business ON business.tenant_id = capability.tenant_id AND business.deleted_at IS NULL").
			Where("capability.tenant_id = ? AND capability.capability = ? AND capability.status IN ?", tenantID, "supplier", []string{"active", "suspended"}).
			Where("capability.deleted_at IS NULL").
			Where("tenant.status = ?", "active").
			Where("business.business_type IN ? AND business.status IN ?", allowed, []string{"active", "suspended"}).
			Count(&count).Error
		if err != nil || count == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前商户没有此供应业态的历史查询权限"})
			return
		}
		c.Next()
	}
}
