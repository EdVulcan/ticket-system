package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func TestXiaohongshuMiniappLoginAndCatalogAreChannelScoped(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-storefront"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-storefront", "app-secret"); err != nil {
		t.Fatal(err)
	}
	if err := (&ChannelService{}).AddMapping(tenantID, &model.ChannelProductMapping{
		ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-TICKET-1",
		DisplayName: "小红书成人票", ChannelSaleCents: 7900,
	}); err != nil {
		t.Fatal(err)
	}

	otherTenantID, otherProductID := seedSellableProduct(t, "unlimited", 0)
	otherAccount := model.ChannelAccount{Code: "xiaohongshu-other"}
	if err := (&ChannelService{}).CreateXiaohongshu(otherTenantID, &otherAccount, "miniapp-other", "other-secret"); err != nil {
		t.Fatal(err)
	}
	if err := (&ChannelService{}).AddMapping(otherTenantID, &model.ChannelProductMapping{
		ChannelAccountID: otherAccount.ID, ProductID: otherProductID, ExternalCode: "XHS-OTHER",
		DisplayName: "其他景区票", ChannelSaleCents: 9900,
	}); err != nil {
		t.Fatal(err)
	}

	server := miniappLoginServer(t)
	defer server.Close()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	miniapp := NewMiniappService()
	miniapp.Now = func() time.Time { return now }
	miniapp.NewXiaohongshuClient = func(appID, secret string) *xiaohongshu.Client {
		if appID != "miniapp-storefront" || secret != "app-secret" {
			t.Fatalf("appid=%q secret=%q", appID, secret)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}

	login, err := miniapp.LoginXiaohongshu(context.Background(), "miniapp-storefront", "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || !login.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("login=%+v", login)
	}
	var customer model.MiniappCustomer
	if err := model.DB.First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	if customer.TenantID != tenantID || customer.ChannelAccountID != account.ID ||
		strings.Contains(customer.OpenIDCiphertext, "OPEN-1") || strings.Contains(customer.SessionKeyCiphertext, "SESSION-1") ||
		customer.SessionTokenHash == login.Token {
		t.Fatalf("customer=%+v", customer)
	}
	if plain, err := utils.DecryptAES(customer.SessionKeyCiphertext); err != nil || plain != "SESSION-1" {
		t.Fatalf("session key=%q err=%v", plain, err)
	}

	authenticated, err := miniapp.Authenticate(login.Token)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := miniapp.ListCatalog(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Products) != 1 || catalog.Products[0].Name != "小红书成人票" || catalog.Products[0].PriceCents != 7900 {
		t.Fatalf("catalog=%+v", catalog)
	}
}

func TestXiaohongshuMiniappSessionFailsClosedWhenChannelIsDisabled(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-disabled"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-disabled", "app-secret"); err != nil {
		t.Fatal(err)
	}
	server := miniappLoginServer(t)
	defer server.Close()
	miniapp := NewMiniappService()
	miniapp.NewXiaohongshuClient = func(appID, secret string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	login, err := miniapp.LoginXiaohongshu(context.Background(), "miniapp-disabled", "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&ChannelService{}).SetStatus(tenantID, account.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := miniapp.Authenticate(login.Token); err != ErrMiniappUnavailable {
		t.Fatalf("error=%v", err)
	}
}

func miniappLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/session":
			if r.URL.Query().Get("code") != "login-code" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"openid":"OPEN-1","session_key":"SESSION-1"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
