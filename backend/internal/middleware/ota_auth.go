package middleware

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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
		timestamp := ctx.Query("timestamp")
		nonce := ctx.Query("nonce")

		if appKey == "" || sign == "" || timestamp == "" || nonce == "" {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing app_key, sign, timestamp, or nonce"})
			return
		}
		unixTime, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid timestamp"})
			return
		}
		maxSkew := time.Duration(config.GlobalConfig.Security.OTAMaxClockSkewSeconds) * time.Second
		requestTime := time.Unix(unixTime, 0)
		if delta := time.Since(requestTime); delta > maxSkew || delta < -maxSkew {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "request timestamp expired"})
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
		calculatedSign := strings.ToUpper(utils.SignParams(params, tenant.SecretKey))
		providedSign := strings.ToUpper(sign)
		if len(calculatedSign) != len(providedSign) || subtle.ConstantTimeCompare([]byte(calculatedSign), []byte(providedSign)) != 1 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		nonceRecord := model.OTANonce{TenantID: tenant.ID, Nonce: nonce, ExpiresAt: time.Now().Add(maxSkew)}
		if err := model.Write(func(tx *gorm.DB) error {
			if err := tx.Where("expires_at < ?", time.Now()).Delete(&model.OTANonce{}).Error; err != nil {
				return err
			}
			return tx.Create(&nonceRecord).Error
		}); err != nil {
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "replayed request"})
			return
		}

		// 4. Set Context
		ctx.Set("tenant_id", tenant.ID)
		ctx.Set("tenant", tenant)

		ctx.Next()
	}
}
