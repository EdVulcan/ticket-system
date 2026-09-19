package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WechatMiniappHTTPLoginAdapter exchanges the one-time wx.login code through
// WeChat's official jscode2session endpoint. AppSecret is supplied by the
// storefront service for the duration of the request and is never returned to
// the mini program.
type WechatMiniappHTTPLoginAdapter struct {
	HTTP    *http.Client
	BaseURL string
}

type wechatMiniappLoginResponse struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

func (a *WechatMiniappHTTPLoginAdapter) ExchangeCode(ctx context.Context, request WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
	appID := strings.TrimSpace(request.AppID)
	secret := strings.TrimSpace(request.AppSecret)
	code := strings.TrimSpace(request.Code)
	if appID == "" || secret == "" || code == "" {
		return WechatMiniappLoginIdentity{}, fmt.Errorf("%w: wechat login credentials are incomplete", ErrCommerceStorefrontInvalid)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.weixin.qq.com"
	}
	query := url.Values{}
	query.Set("appid", appID)
	query.Set("secret", secret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")
	requestURL := baseURL + "/sns/jscode2session?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return WechatMiniappLoginIdentity{}, fmt.Errorf("wechat login request: %w", err)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return WechatMiniappLoginIdentity{}, fmt.Errorf("wechat login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WechatMiniappLoginIdentity{}, fmt.Errorf("wechat login returned HTTP %d", resp.StatusCode)
	}
	var payload wechatMiniappLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return WechatMiniappLoginIdentity{}, fmt.Errorf("wechat login response: %w", err)
	}
	if payload.ErrCode != 0 {
		message := strings.TrimSpace(payload.ErrMsg)
		if message == "" {
			message = "provider rejected login"
		}
		return WechatMiniappLoginIdentity{}, fmt.Errorf("wechat login rejected (%d): %s", payload.ErrCode, message)
	}
	// Prefer the stable union id when WeChat supplies one, but fall back to the
	// app-scoped open id. The service hashes the selected subject before storage.
	subject := strings.TrimSpace(payload.UnionID)
	if subject == "" {
		subject = strings.TrimSpace(payload.OpenID)
	}
	if subject == "" || strings.TrimSpace(payload.SessionKey) == "" {
		return WechatMiniappLoginIdentity{}, errors.New("wechat login response did not contain an identity")
	}
	return WechatMiniappLoginIdentity{Subject: subject, OpenID: payload.OpenID}, nil
}
