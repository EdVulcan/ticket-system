package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type wechatPhoneTestTokenProvider struct {
	token WechatAccessToken
	err   error
}

func (p wechatPhoneTestTokenProvider) FetchAccessToken(context.Context, string, string) (WechatAccessToken, error) {
	return p.token, p.err
}

func newWechatPhoneTestClient(server *httptest.Server) *WechatPhoneAuthClient {
	return &WechatPhoneAuthClient{
		HTTPClient:          server.Client(),
		BaseURL:             server.URL,
		AccessTokenProvider: wechatPhoneTestTokenProvider{token: WechatAccessToken{Token: "test-access-token", ExpiresAt: time.Now().Add(time.Hour)}},
	}
}

func TestWechatPhoneAuthExchangePhoneCodeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wechatPhoneNumberPath {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if got := r.URL.Query().Get("access_token"); got != "test-access-token" {
			t.Fatalf("access_token=%q", got)
		}
		var request struct {
			Code   string `json:"code"`
			OpenID string `json:"openid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Code != "one-time-code" || request.OpenID != "" {
			t.Fatalf("request=%+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","phone_info":{"phoneNumber":"+86 13800138000","purePhoneNumber":"13800138000","countryCode":"86","openid":"openid-1","watermark":{"timestamp":1780000000,"appid":"wx-test"}}}`))
	}))
	defer server.Close()

	result, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "one-time-code", OpenID: "openid-1"})
	if err != nil {
		t.Fatalf("ExchangePhoneCode() error = %v", err)
	}
	if result.PhoneNumber != "+86 13800138000" || result.PurePhoneNumber != "13800138000" || result.WatermarkAppID != "wx-test" || result.OpenID != "openid-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWechatPhoneAuthRejectsAppIDMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"phone_info":{"phoneNumber":"13800138000","watermark":{"appid":"wx-other"}}}`))
	}))
	defer server.Close()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	if !errors.Is(err, ErrWechatPhoneAppIDMismatch) {
		t.Fatalf("error=%v, want appid mismatch", err)
	}
}

func TestWechatPhoneAuthAcceptsOfficialResponseWithoutOpenID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"phone_info":{"phoneNumber":"13800138000","purePhoneNumber":"13800138000","countryCode":"86","watermark":{"appid":"wx-test"}}}`))
	}))
	defer server.Close()

	result, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code", OpenID: "openid-1"})
	if err != nil {
		t.Fatalf("ExchangePhoneCode() error = %v", err)
	}
	if result.PurePhoneNumber != "13800138000" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWechatPhoneAuthRejectsPresentMismatchedOpenID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"phone_info":{"phoneNumber":"13800138000","purePhoneNumber":"13800138000","countryCode":"86","openid":"openid-other","watermark":{"appid":"wx-test"}}}`))
	}))
	defer server.Close()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code", OpenID: "openid-1"})
	if !errors.Is(err, ErrWechatPhoneOpenIDMismatch) {
		t.Fatalf("error=%v, want present openid mismatch", err)
	}
}

func TestWechatPhoneAuthMapsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	}))
	defer server.Close()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	var providerErr *WechatPhoneProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != 40029 || !errors.Is(err, ErrWechatPhoneProvider) {
		t.Fatalf("error=%v, provider error=%+v", err, providerErr)
	}
}

func TestWechatPhoneAuthMapsNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()
	client := newWechatPhoneTestClient(server)

	_, err := client.ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	if !errors.Is(err, ErrWechatPhoneHTTP) {
		t.Fatalf("error=%v, want network error", err)
	}
}

func TestWechatPhoneAuthHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client must stop waiting on cancellation. Keep a bounded fallback
		// so httptest.Server.Close cannot hang if a transport implementation
		// delays propagating cancellation to the handler.
		select {
		case <-r.Context().Done():
		case <-time.After(250 * time.Millisecond):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(ctx, WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}

func TestWechatPhoneAuthRejectsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	if !errors.Is(err, ErrWechatPhoneEmptyResponse) {
		t.Fatalf("error=%v, want empty response", err)
	}
}

func TestWechatPhoneAuthRejectsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	}))
	defer server.Close()

	_, err := newWechatPhoneTestClient(server).ExchangePhoneCode(context.Background(), WechatPhoneAuthRequest{AppID: "wx-test", AppSecret: "secret", Code: "code"})
	var httpErr *WechatPhoneHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadGateway || !errors.Is(err, ErrWechatPhoneHTTP) {
		t.Fatalf("error=%v, http error=%+v", err, httpErr)
	}
}
