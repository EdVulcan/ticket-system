package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxLoginBodyBytes = 64 << 10

type loginFailureWindow struct {
	count   int
	expires time.Time
}

type loginFailureLimiter struct {
	mu      sync.Mutex
	windows map[string]loginFailureWindow
}

func RequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if maxBytes <= 0 {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "请求限制配置错误"})
			return
		}
		if ctx.Request.ContentLength > maxBytes {
			ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求内容过大"})
			return
		}
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxBytes)
		ctx.Next()
	}
}

func LoginRateLimit() gin.HandlerFunc {
	limiter := &loginFailureLimiter{windows: make(map[string]loginFailureWindow)}
	return limiter.handle
}

func MiniappLoginRateLimit() gin.HandlerFunc {
	limiter := &loginFailureLimiter{windows: make(map[string]loginFailureWindow)}
	return func(ctx *gin.Context) {
		key := "ip:" + requestRemoteIP(ctx.Request)
		now := time.Now()
		if limiter.blocked(key, 60, now) {
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "登录请求过于频繁，请稍后重试"})
			return
		}
		ctx.Next()
		if ctx.Writer.Status() == http.StatusUnauthorized {
			limiter.record(key, now)
		}
	}
}

// ProvisioningRateLimit protects the public installer handshake before a
// device has credentials. It deliberately keeps only a short-lived hash of
// the activation token in memory; the raw binding code is never logged or
// retained by the limiter. Limits are conservative because a legitimate
// installer normally performs one claim plus, at most, a handful of retries.
func ProvisioningRateLimit() gin.HandlerFunc {
	limiter := &loginFailureLimiter{windows: make(map[string]loginFailureWindow)}
	return func(ctx *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, maxLoginBodyBytes+1))
		if err != nil || len(body) > maxLoginBodyBytes {
			ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "安装绑定请求内容过大"})
			return
		}
		ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
		ipKey := "provision-ip:" + requestRemoteIP(ctx.Request)
		tokenKey := ""
		var input struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(body, &input) == nil && strings.TrimSpace(input.Token) != "" {
			digest := sha256.Sum256([]byte(strings.TrimSpace(input.Token)))
			tokenKey = "provision-token:" + hex.EncodeToString(digest[:])
		}
		now := time.Now()
		if limiter.blocked(ipKey, 30, now) || (tokenKey != "" && limiter.blocked(tokenKey, 8, now)) {
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "安装绑定请求过于频繁，请稍后重试"})
			return
		}
		limiter.record(ipKey, now)
		if tokenKey != "" {
			limiter.record(tokenKey, now)
		}
		ctx.Next()
	}
}

func (l *loginFailureLimiter) handle(ctx *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, maxLoginBodyBytes+1))
	if err != nil || len(body) > maxLoginBodyBytes {
		ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "登录请求内容过大"})
		return
	}
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	identity := loginIdentity(ctx.Request.URL.Path, body)
	ip := requestRemoteIP(ctx.Request)
	now := time.Now()
	if l.blocked("ip:"+ip, 60, now) || l.blocked("identity:"+identity, 10, now) {
		ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "登录失败次数过多，请稍后再试"})
		return
	}
	ctx.Next()
	if ctx.Writer.Status() == http.StatusUnauthorized {
		l.record("ip:"+ip, now)
		l.record("identity:"+identity, now)
	} else if ctx.Writer.Status() >= 200 && ctx.Writer.Status() < 300 {
		l.clear("identity:" + identity)
	}
}

func loginIdentity(path string, body []byte) string {
	var values map[string]string
	_ = json.Unmarshal(body, &values)
	systemCode := strings.ToLower(strings.TrimSpace(values["system_code"]))
	account := strings.ToLower(strings.TrimSpace(values["username"]))
	if account == "" {
		account = strings.ToLower(strings.TrimSpace(values["job_number"]))
	}
	return strings.ToLower(strings.TrimSpace(path)) + "|" + systemCode + "|" + account
}

func (l *loginFailureLimiter) blocked(key string, limit int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	window, ok := l.windows[key]
	if !ok || !now.Before(window.expires) {
		delete(l.windows, key)
		return false
	}
	return window.count >= limit
}

func (l *loginFailureLimiter) record(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	window, ok := l.windows[key]
	if !ok || !now.Before(window.expires) {
		window = loginFailureWindow{expires: now.Add(time.Minute)}
	}
	window.count++
	l.windows[key] = window
}

func (l *loginFailureLimiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, key)
}
