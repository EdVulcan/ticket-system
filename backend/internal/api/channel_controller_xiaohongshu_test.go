package api

import (
	"errors"
	"net/http"
	"testing"

	"ticket-backend/internal/xiaohongshu"
)

func TestXiaohongshuUpstreamErrorExplainsMissingTradePermission(t *testing.T) {
	status, message := xiaohongshuUpstreamError(&xiaohongshu.APIError{Code: 420156, Message: "未开通交易权限"})
	if status != http.StatusConflict || message != "当前小程序尚未开通交易权限，请先在小红书开放平台开通本地生活交易能力" {
		t.Fatalf("status=%d message=%q", status, message)
	}
}

func TestXiaohongshuUpstreamErrorTreatsLockFailureAsTemporary(t *testing.T) {
	status, message := xiaohongshuUpstreamError(&xiaohongshu.APIError{Code: 12, Message: "redis上锁失败"})
	if status != http.StatusServiceUnavailable || message != "小红书接口繁忙，请稍后重新加载" {
		t.Fatalf("status=%d message=%q", status, message)
	}
}

func TestXiaohongshuUpstreamErrorKeepsUnknownFailuresGeneric(t *testing.T) {
	status, message := xiaohongshuUpstreamError(errors.New("transport failed"))
	if status != http.StatusBadGateway || message != "小红书接口请求失败，请稍后重试" {
		t.Fatalf("status=%d message=%q", status, message)
	}
}
