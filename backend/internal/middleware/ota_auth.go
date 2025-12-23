package middleware

import (
	"net/http"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"

	"github.com/gin-gonic/gin"
)

// OTASignMiddleware verifies the signature of OTA requests
// Required query/form params: app_key, sign, timestamp (optional but good practice)
func OTASignMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 1. Get Params
		var params = make(map[string]string)

		// Bind Query
		for k, v := range ctx.Request.URL.Query() {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}

		// Bind PostForm (if any)
		if err := ctx.Request.ParseForm(); err == nil {
			for k, v := range ctx.Request.PostForm {
				if len(v) > 0 {
					params[k] = v[0]
				}
			}
		}

		// If JSON body, we usually don't sign JSON content deeply in this simple middleware,
		// or we require params in Query String even for POST.
		// Standard OTA usually puts auth params in Query, and data in Body.
		// Verification strategy: Sign only the Query Params including app_key, timestamp, noncestr.
		// OR Sign all top-level string params.
		// Let's stick to: Auth via Query Params (app_key, sign, timestamp, nonce) + Secret.

		appKey := ctx.Query("app_key")
		sign := ctx.Query("sign")

		if appKey == "" || sign == "" {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing app_key or sign"})
			return
		}

		// 2. Find Tenant
		var tenant model.Tenant
		if err := model.DB.Where("system_code = ?", appKey).First(&tenant).Error; err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid app_key"})
			return
		}

		// 3. Verify Signature
		// Re-calculate sign: MD5(k=v&...&secret_key=SECRET)
		// We sign ALL query params except 'sign' itself.
		calculatedSign := utils.SignParams(params, tenant.SecretKey)

		// Case-insensitive comparison often needed for MD5
		if calculatedSign != sign && calculatedSign != utils.MD5(sign) { // strict match usually
			// My implementation returns lowercase hex.
			if calculatedSign != sign {
				// Debug mode:
				// ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature", "debug_sign": calculatedSign})
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
				return
			}
		}

		// 4. Set Context
		ctx.Set("tenant_id", tenant.ID)
		ctx.Set("tenant", tenant)

		ctx.Next()
	}
}
