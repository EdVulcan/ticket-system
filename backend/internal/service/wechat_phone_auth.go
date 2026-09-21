package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	wechatPhoneDefaultBaseURL   = "https://api.weixin.qq.com"
	wechatPhoneTokenPath        = "/cgi-bin/token"
	wechatPhoneNumberPath       = "/wxa/business/getuserphonenumber"
	wechatPhoneResponseMaxBytes = 1 << 20
	wechatPhoneTokenSkew        = 30 * time.Second
)

var (
	ErrWechatPhoneInvalidRequest = errors.New("微信手机号授权参数无效")
	ErrWechatPhoneProvider       = errors.New("微信手机号授权平台错误")
	ErrWechatPhoneHTTP           = errors.New("微信手机号授权网络请求失败")
	ErrWechatPhoneEmptyResponse  = errors.New("微信手机号授权返回为空")
	ErrWechatPhoneMalformed      = errors.New("微信手机号授权返回格式无效")
	ErrWechatPhoneAppIDMismatch  = errors.New("微信手机号授权应用不匹配")
	ErrWechatPhoneOpenIDMismatch = errors.New("微信手机号授权用户不匹配")
	ErrWechatPhoneMissing        = errors.New("微信手机号授权未返回手机号")
)

// WechatPhoneAuthRequest contains the current channel account and the
// one-time code returned by the platform's getPhoneNumber button. AppSecret
// and Code must never be logged or persisted by this adapter.
type WechatPhoneAuthRequest struct {
	AppID     string
	AppSecret string
	Code      string
	OpenID    string
}

// WechatPhoneVerificationResult is platform-authenticated evidence. It is
// intentionally separate from a member/contact record; callers must pass the
// verified phone to MemberService.VerifyTrustedPhone in their own transaction.
type WechatPhoneVerificationResult struct {
	AppID             string
	OpenID            string
	PhoneNumber       string
	PurePhoneNumber   string
	CountryCode       string
	WatermarkAppID    string
	WatermarkUnixTime int64
}

// WechatPhoneAuthAdapter is the platform boundary used by storefront
// services. Implementations must not create or merge members themselves.
type WechatPhoneAuthAdapter interface {
	ExchangePhoneCode(context.Context, WechatPhoneAuthRequest) (WechatPhoneVerificationResult, error)
}

// WechatPhoneAuthFunc makes the adapter easy to replace in service tests.
type WechatPhoneAuthFunc func(context.Context, WechatPhoneAuthRequest) (WechatPhoneVerificationResult, error)

func (f WechatPhoneAuthFunc) ExchangePhoneCode(ctx context.Context, request WechatPhoneAuthRequest) (WechatPhoneVerificationResult, error) {
	return f(ctx, request)
}

// WechatAccessToken is the short-lived credential returned by WeChat.
type WechatAccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// WechatAccessTokenProvider isolates credential exchange from phone-code
// exchange. A caller may inject a provider backed by its own secret manager.
type WechatAccessTokenProvider interface {
	FetchAccessToken(context.Context, string, string) (WechatAccessToken, error)
}

// WechatAccessTokenCache is deliberately keyed only by AppID. Implementations
// must keep the token value private and must not expose it in logs or APIs.
type WechatAccessTokenCache interface {
	Get(string) (WechatAccessToken, bool)
	Set(string, WechatAccessToken)
}

// WechatMemoryAccessTokenCache is a small process-local cache suitable for a
// single backend instance. Deployments with multiple instances can inject a
// shared cache without changing the phone adapter contract.
type WechatMemoryAccessTokenCache struct {
	mu      sync.Mutex
	entries map[string]WechatAccessToken
}

func (c *WechatMemoryAccessTokenCache) Get(appID string) (WechatAccessToken, bool) {
	if c == nil {
		return WechatAccessToken{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[appID]
	return entry, ok
}

func (c *WechatMemoryAccessTokenCache) Set(appID string, token WechatAccessToken) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]WechatAccessToken)
	}
	c.entries[appID] = token
}

// WechatPhoneAuthClient is the default HTTP implementation. HTTPClient,
// AccessTokenProvider and AccessTokenCache are all injectable for tests and
// for deployments that centralize credential caching.
type WechatPhoneAuthClient struct {
	HTTPClient          *http.Client
	BaseURL             string
	AccessTokenProvider WechatAccessTokenProvider
	AccessTokenCache    WechatAccessTokenCache
	Now                 func() time.Time
}

func (c *WechatPhoneAuthClient) ExchangePhoneCode(ctx context.Context, request WechatPhoneAuthRequest) (WechatPhoneVerificationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request.AppID = strings.TrimSpace(request.AppID)
	request.AppSecret = strings.TrimSpace(request.AppSecret)
	request.Code = strings.TrimSpace(request.Code)
	request.OpenID = strings.TrimSpace(request.OpenID)
	if request.AppID == "" || request.AppSecret == "" || request.Code == "" {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneInvalidRequest
	}

	token, err := c.accessToken(ctx, request.AppID, request.AppSecret)
	if err != nil {
		return WechatPhoneVerificationResult{}, err
	}
	if strings.TrimSpace(token.Token) == "" {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneProvider
	}

	baseURL := c.baseURL()
	endpoint, err := url.Parse(baseURL + wechatPhoneNumberPath)
	if err != nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneHTTP
	}
	query := endpoint.Query()
	query.Set("access_token", token.Token)
	endpoint.RawQuery = query.Encode()
	// getuserphonenumber accepts the one-time authorization code as its
	// request body. The session openid is retained only for an optional
	// consistency check against compatible provider responses; it is not a
	// request parameter of the official API.
	payload, err := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: request.Code})
	if err != nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneHTTP
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(payload)))
	if err != nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneHTTP
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.client().Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return WechatPhoneVerificationResult{}, ctxErr
		}
		return WechatPhoneVerificationResult{}, ErrWechatPhoneHTTP
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, wechatPhoneResponseMaxBytes))
	if err != nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneHTTP
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return WechatPhoneVerificationResult{}, &WechatPhoneHTTPError{StatusCode: response.StatusCode}
	}
	if len(body) == 0 {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneEmptyResponse
	}
	var providerResponse wechatPhoneResponse
	if err := json.Unmarshal(body, &providerResponse); err != nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneMalformed
	}
	if providerResponse.ErrCode != 0 {
		return WechatPhoneVerificationResult{}, &WechatPhoneProviderError{Code: providerResponse.ErrCode, Message: providerResponse.ErrMsg}
	}
	if providerResponse.PhoneInfo == nil {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneMissing
	}
	info := providerResponse.PhoneInfo
	if strings.TrimSpace(info.Watermark.AppID) == "" || info.Watermark.AppID != request.AppID {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneAppIDMismatch
	}
	// The official getuserphonenumber response does not require an openid in
	// phone_info. Some provider-compatible responses include it, so compare it
	// when present, but never reject a valid one-time phone code merely because
	// the optional field is absent. The code itself is the platform assertion;
	// watermark.appid remains mandatory to prevent cross-app payload reuse.
	if request.OpenID != "" && strings.TrimSpace(info.OpenID) != "" && info.OpenID != request.OpenID {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneOpenIDMismatch
	}
	if strings.TrimSpace(info.PurePhoneNumber) == "" && strings.TrimSpace(info.PhoneNumber) == "" {
		return WechatPhoneVerificationResult{}, ErrWechatPhoneMissing
	}
	return WechatPhoneVerificationResult{
		AppID:             request.AppID,
		OpenID:            info.OpenID,
		PhoneNumber:       strings.TrimSpace(info.PhoneNumber),
		PurePhoneNumber:   strings.TrimSpace(info.PurePhoneNumber),
		CountryCode:       strings.TrimSpace(info.CountryCode),
		WatermarkAppID:    strings.TrimSpace(info.Watermark.AppID),
		WatermarkUnixTime: info.Watermark.Timestamp,
	}, nil
}

func (c *WechatPhoneAuthClient) accessToken(ctx context.Context, appID, appSecret string) (WechatAccessToken, error) {
	now := c.now()
	if c.AccessTokenCache != nil {
		if token, ok := c.AccessTokenCache.Get(appID); ok && strings.TrimSpace(token.Token) != "" && token.ExpiresAt.After(now.Add(wechatPhoneTokenSkew)) {
			return token, nil
		}
	}
	provider := c.AccessTokenProvider
	if provider == nil {
		provider = wechatHTTPAccessTokenProvider{HTTPClient: c.client(), BaseURL: c.baseURL()}
	}
	token, err := provider.FetchAccessToken(ctx, appID, appSecret)
	if err != nil {
		return WechatAccessToken{}, err
	}
	if strings.TrimSpace(token.Token) == "" || !token.ExpiresAt.After(now) {
		return WechatAccessToken{}, ErrWechatPhoneProvider
	}
	if c.AccessTokenCache != nil {
		c.AccessTokenCache.Set(appID, token)
	}
	return token, nil
}

func (c *WechatPhoneAuthClient) client() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{}
}

func (c *WechatPhoneAuthClient) baseURL() string {
	if c != nil && strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	}
	return wechatPhoneDefaultBaseURL
}

func (c *WechatPhoneAuthClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

type wechatHTTPAccessTokenProvider struct {
	HTTPClient *http.Client
	BaseURL    string
}

func (p wechatHTTPAccessTokenProvider) FetchAccessToken(ctx context.Context, appID, appSecret string) (WechatAccessToken, error) {
	endpoint, err := url.Parse(strings.TrimRight(p.BaseURL, "/") + wechatPhoneTokenPath)
	if err != nil {
		return WechatAccessToken{}, ErrWechatPhoneHTTP
	}
	query := endpoint.Query()
	query.Set("grant_type", "client_credential")
	query.Set("appid", appID)
	query.Set("secret", appSecret)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return WechatAccessToken{}, ErrWechatPhoneHTTP
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return WechatAccessToken{}, ctxErr
		}
		return WechatAccessToken{}, ErrWechatPhoneHTTP
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, wechatPhoneResponseMaxBytes))
	if err != nil {
		return WechatAccessToken{}, ErrWechatPhoneHTTP
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return WechatAccessToken{}, &WechatPhoneHTTPError{StatusCode: response.StatusCode}
	}
	if len(body) == 0 {
		return WechatAccessToken{}, ErrWechatPhoneEmptyResponse
	}
	var providerResponse wechatAccessTokenResponse
	if err := json.Unmarshal(body, &providerResponse); err != nil {
		return WechatAccessToken{}, ErrWechatPhoneMalformed
	}
	if providerResponse.ErrCode != 0 {
		return WechatAccessToken{}, &WechatPhoneProviderError{Code: providerResponse.ErrCode, Message: providerResponse.ErrMsg}
	}
	if strings.TrimSpace(providerResponse.AccessToken) == "" || providerResponse.ExpiresIn <= 0 {
		return WechatAccessToken{}, ErrWechatPhoneProvider
	}
	return WechatAccessToken{Token: providerResponse.AccessToken, ExpiresAt: time.Now().Add(time.Duration(providerResponse.ExpiresIn) * time.Second)}, nil
}

type wechatAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type wechatPhoneResponse struct {
	ErrCode   int              `json:"errcode"`
	ErrMsg    string           `json:"errmsg"`
	PhoneInfo *wechatPhoneInfo `json:"phone_info"`
}

type wechatPhoneInfo struct {
	PhoneNumber     string `json:"phoneNumber"`
	PurePhoneNumber string `json:"purePhoneNumber"`
	CountryCode     string `json:"countryCode"`
	OpenID          string `json:"openid"`
	Watermark       struct {
		Timestamp int64  `json:"timestamp"`
		AppID     string `json:"appid"`
	} `json:"watermark"`
}

type WechatPhoneProviderError struct {
	Code    int
	Message string
}

func (e *WechatPhoneProviderError) Error() string {
	return fmt.Sprintf("微信手机号授权平台错误(code=%d)", e.Code)
}
func (e *WechatPhoneProviderError) Unwrap() error { return ErrWechatPhoneProvider }

type WechatPhoneHTTPError struct{ StatusCode int }

func (e *WechatPhoneHTTPError) Error() string {
	return fmt.Sprintf("微信手机号授权网络请求失败(status=%d)", e.StatusCode)
}
func (e *WechatPhoneHTTPError) Unwrap() error { return ErrWechatPhoneHTTP }

var _ WechatPhoneAuthAdapter = (*WechatPhoneAuthClient)(nil)
