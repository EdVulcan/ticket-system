package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const staleChannelRequestAfter = 5 * time.Minute

type channelCaptureWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *channelCaptureWriter) Write(data []byte) (int, error) {
	_, _ = w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *channelCaptureWriter) WriteString(value string) (int, error) {
	_, _ = w.body.WriteString(value)
	return w.ResponseWriter.WriteString(value)
}

// ChannelAuthMiddleware authenticates a channel account using an independent
// secret and a signature that covers the exact request body.
func ChannelAuthMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		code := strings.TrimSpace(ctx.Param("code"))
		if code == "" {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "channel code is required"})
			return
		}
		timestamp := strings.TrimSpace(ctx.GetHeader("X-Channel-Timestamp"))
		nonce := strings.TrimSpace(ctx.GetHeader("X-Channel-Nonce"))
		requestID := strings.TrimSpace(ctx.GetHeader("X-Channel-Request-Id"))
		provided := strings.TrimSpace(ctx.GetHeader("X-Channel-Signature"))
		if timestamp == "" || nonce == "" || requestID == "" || provided == "" || len(nonce) > 120 || len(requestID) > 120 || len(provided) > 128 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing channel authentication headers"})
			return
		}
		unixTime, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid channel timestamp"})
			return
		}
		maxSkew := time.Duration(config.GlobalConfig.Security.OTAMaxClockSkewSeconds) * time.Second
		if delta := time.Since(time.Unix(unixTime, 0)); delta > maxSkew || delta < -maxSkew {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "channel timestamp expired"})
			return
		}
		account, secret, err := (&service.ChannelService{}).GetByCode(code)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid channel account"})
			return
		}
		remoteIP := requestRemoteIP(ctx.Request)
		if !channelIPAllowed(account.AllowedIPsJSON, remoteIP) {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "channel client IP is not allowed"})
			return
		}
		if !channelPermissionAllows(account.PermissionsJSON, channelPermissionForPath(ctx.Request.URL.Path)) {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "channel permission denied"})
			return
		}
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 2<<20)
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "channel request body is too large"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read request body"})
			return
		}
		ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
		bodyHash := sha256.Sum256(body)
		canonicalQuery := ctx.Request.URL.Query().Encode()
		canonical := strings.Join([]string{timestamp, nonce, requestID, ctx.Request.Method, ctx.Request.URL.Path, canonicalQuery, hex.EncodeToString(bodyHash[:])}, "\n")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(canonical))
		calculated := hex.EncodeToString(mac.Sum(nil))
		if len(calculated) != len(provided) || subtle.ConstantTimeCompare([]byte(strings.ToLower(calculated)), []byte(strings.ToLower(provided))) != 1 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid channel signature"})
			return
		}
		var replayResponse string
		replayStatus := http.StatusOK
		var requestLeaseAt *time.Time
		if err := model.Write(func(tx *gorm.DB) error {
			now := time.Now()
			if err := tx.Where("channel_account_id = ? AND expires_at < ?", account.ID, now).Delete(&model.ChannelNonce{}).Error; err != nil {
				return err
			}
			var existing model.ChannelRequest
			err := tx.Where("channel_account_id = ? AND request_id = ?", account.ID, requestID).First(&existing).Error
			if err == nil {
				if existing.BodyHash != hex.EncodeToString(bodyHash[:]) || existing.Endpoint != ctx.Request.URL.Path || (existing.Nonce != "" && existing.Nonce != nonce) {
					return errors.New("channel request id reused with different data")
				}
				if existing.Status == "completed" && existing.ResponseJSON != "" {
					replayResponse = existing.ResponseJSON
					if existing.ResponseStatus > 0 {
						replayStatus = existing.ResponseStatus
					}
					return nil
				}
				if existing.Status == "failed" {
					return errors.New("channel request failed and requires retry authorization")
				}
				leaseAt := existing.CreatedAt
				if existing.LockedAt != nil {
					leaseAt = *existing.LockedAt
				}
				if existing.Status == "processing" && !leaseAt.IsZero() && now.Sub(leaseAt) < staleChannelRequestAfter {
					return errors.New("channel request is already processing")
				}
				requestLeaseAt = &now
				return tx.Model(&existing).Updates(map[string]interface{}{
					"status": "processing", "locked_at": now, "response_json": "", "response_status": http.StatusOK,
					"attempt_count": gorm.Expr("attempt_count + 1"), "last_attempt_at": now, "completed_at": nil,
				}).Error
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var nonceRecord model.ChannelNonce
			if err := tx.Where("channel_account_id = ? AND nonce = ?", account.ID, nonce).First(&nonceRecord).Error; err == nil {
				return errors.New("channel nonce has already been used")
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if account.RateLimitPerMin > 0 {
				var recent int64
				if err := tx.Model(&model.ChannelRequest{}).Where("channel_account_id = ? AND created_at >= ?", account.ID, now.Add(-time.Minute)).Count(&recent).Error; err != nil {
					return err
				}
				if recent >= int64(account.RateLimitPerMin) {
					return errors.New("channel rate limit exceeded")
				}
			}
			if err := tx.Create(&model.ChannelNonce{ChannelAccountID: account.ID, Nonce: nonce, RequestID: requestID, ExpiresAt: now.Add(maxSkew)}).Error; err != nil {
				return errors.New("channel nonce has already been used")
			}
			requestLeaseAt = &now
			return tx.Create(&model.ChannelRequest{ChannelAccountID: account.ID, RequestID: requestID, Nonce: nonce, Endpoint: ctx.Request.URL.Path, BodyHash: hex.EncodeToString(bodyHash[:]), Status: "processing", ResponseStatus: http.StatusOK, RemoteIP: remoteIP, AttemptCount: 1, LastAttemptAt: &now, LockedAt: requestLeaseAt}).Error
		}); err != nil {
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if replayResponse != "" {
			ctx.Header("Content-Type", "application/json; charset=utf-8")
			ctx.Status(replayStatus)
			_, _ = ctx.Writer.WriteString(replayResponse)
			ctx.Abort()
			return
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(&account).Updates(map[string]interface{}{"last_used_at": time.Now()}).Error
		})
		ctx.Set("tenant_id", account.TenantID)
		ctx.Set("channel_account_id", account.ID)
		ctx.Set("channel_code", account.Code)
		ctx.Set("channel_type", account.Type)
		capture := &channelCaptureWriter{ResponseWriter: ctx.Writer}
		ctx.Writer = capture
		ctx.Next()
		status := "completed"
		if ctx.Writer.Status() >= http.StatusInternalServerError {
			status = "failed"
		}
		responseStatus := ctx.Writer.Status()
		completedAt := time.Now()
		var completedAtValue interface{} = completedAt
		if status == "failed" {
			completedAtValue = nil
		}
		_ = model.Write(func(tx *gorm.DB) error {
			query := tx.Model(&model.ChannelRequest{}).
				Where("channel_account_id = ? AND request_id = ? AND status = ?", account.ID, requestID, "processing")
			if requestLeaseAt != nil {
				query = query.Where("locked_at = ?", *requestLeaseAt)
			}
			return query.Updates(map[string]interface{}{"status": status, "response_json": capture.body.String(), "response_status": responseStatus, "completed_at": completedAtValue, "locked_at": nil}).Error
		})
	}
}

func channelPermissionForPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/products"):
		return "products:read"
	case strings.HasSuffix(path, "/orders/create"):
		return "orders:create"
	case strings.HasSuffix(path, "/orders/cancel"):
		return "orders:cancel"
	case strings.HasSuffix(path, "/orders/refund"):
		return "orders:refund"
	case strings.HasSuffix(path, "/orders/query"):
		return "orders:query"
	case strings.HasSuffix(path, "/reservations/create"):
		return "inventory:reserve"
	case strings.HasSuffix(path, "/reservations/confirm"):
		return "orders:create"
	case strings.HasSuffix(path, "/reservations/release"):
		return "orders:cancel"
	default:
		return ""
	}
}

func channelPermissionAllows(raw, required string) bool {
	if required == "" {
		return false
	}
	var list []string
	if json.Unmarshal([]byte(raw), &list) == nil {
		for _, permission := range list {
			if permission == "*" || permission == required {
				return true
			}
		}
		return false
	}
	var values map[string]bool
	if json.Unmarshal([]byte(raw), &values) == nil {
		return values["*"] || values[required]
	}
	return false
}

func requestRemoteIP(req *http.Request) string {
	if req == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(req.RemoteAddr)
	}
	peer := net.ParseIP(host)
	if peer == nil || !trustedProxyIP(peer) {
		return host
	}
	forwarded := strings.Split(req.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[i]))
		if candidate == nil || trustedProxyIP(candidate) {
			continue
		}
		return candidate.String()
	}
	return host
}

func trustedProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, raw := range strings.Split(config.GlobalConfig.Server.TrustedProxyCIDRs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if candidate := net.ParseIP(raw); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(raw); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func channelIPAllowed(raw, ip string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return false
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "*" || value == ip {
			return true
		}
		if _, network, err := net.ParseCIDR(value); err == nil && network.Contains(net.ParseIP(ip)) {
			return true
		}
	}
	return false
}
