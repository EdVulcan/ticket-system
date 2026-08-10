package middleware

import (
	"bytes"
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
