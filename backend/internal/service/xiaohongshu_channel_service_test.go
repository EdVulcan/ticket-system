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
)

func TestXiaohongshuChannelCredentialsAreTenantScopedAndEncrypted(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-main", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, "miniapp-1", "app-secret-1", "MessageToken123", "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"); err != nil {
		t.Fatal(err)
	}
	if account.Type != "xiaohongshu" || account.Status != "sandbox" || account.Environment != "sandbox" {
		t.Fatalf("account=%+v", account)
	}
	if account.SecretCiphertext == "" || account.VerifyKeyCiphertext == "" || account.ProtocolConfigCiphertext == "" || strings.Contains(account.SecretCiphertext, "app-secret-1") {
		t.Fatal("xiaohongshu credentials were not encrypted")
	}
	plain, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || plain != "app-secret-1" {
		t.Fatalf("secret=%q err=%v", plain, err)
	}

	rows, err := (&ChannelService{}).List(tenantID)
	if err != nil || len(rows) != 1 || !rows[0].ProtocolConfigured || rows[0].SecretCiphertext != "" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}

	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	duplicate := model.ChannelAccount{Code: "xiaohongshu-other"}
	if err := (&ChannelService{}).CreateXiaohongshu(otherTenantID, &duplicate, "miniapp-1", "other-secret"); err == nil {
		t.Fatal("same xiaohongshu appid was accepted by another tenant")
	}
	if err := (&ChannelService{}).ConfigureXiaohongshuIntegration(otherTenantID, account.ID, "miniapp-forged", "forged-secret", "ForgedToken123", "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"); err == nil {
		t.Fatal("another tenant updated xiaohongshu credentials")
	}
	var stored model.ChannelAccount
	if err := model.DB.First(&stored, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AppID != "miniapp-1" {
		t.Fatalf("appid=%q", stored.AppID)
	}
}

func TestXiaohongshuChannelCredentialsCanBeReplacedWithoutDisclosure(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-main"}
	service := &ChannelService{}
	if err := service.CreateXiaohongshuIntegration(tenantID, &account, "miniapp-old", "old-secret", "OldToken123", "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureXiaohongshuIntegration(tenantID, account.ID, "miniapp-new", "new-secret", "NewToken123", "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefgh123456789"); err != nil {
		t.Fatal(err)
	}
	var stored model.ChannelAccount
	if err := model.DB.First(&stored, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := utils.DecryptAES(stored.SecretCiphertext)
	if err != nil || stored.AppID != "miniapp-new" || plain != "new-secret" || stored.KeyVersion != 2 {
		t.Fatalf("stored=%+v secret=%q err=%v", stored, plain, err)
	}
	if _, err := service.RotateSecret(tenantID, account.ID); err == nil {
		t.Fatal("xiaohongshu secret was replaced with a generated generic channel secret")
	}
}

func TestXiaohongshuMappingMetadataCanBeCorrectedWithinTenant(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-mapping"}
	service := &ChannelService{}
	if err := service.CreateXiaohongshu(tenantID, &account, "miniapp-mapping", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{
		ChannelAccountID: account.ID,
		ProductID:        productID,
		ExternalCode:     "XHS-OLD",
		DisplayName:      "旧展示名",
		ChannelSaleCents: 1,
	}
	if err := service.AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}

	input := ChannelMappingUpdate{
		ExternalCode:     "XHS-NEW",
		DisplayName:      "小红书沙盒联调测试票",
		ChannelSaleCents: 1,
		ChannelCostCents: 1,
		Status:           "active",
	}
	if err := service.UpdateMapping(tenantID, account.ID, mapping.ID, input); err != nil {
		t.Fatal(err)
	}
	var stored model.ChannelProductMapping
	if err := model.DB.First(&stored, mapping.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ProductID != productID || stored.ChannelAccountID != account.ID || stored.ExternalCode != input.ExternalCode || stored.DisplayName != input.DisplayName || stored.ChannelSaleCents != 1 || stored.Status != "active" {
		t.Fatalf("stored=%+v", stored)
	}

	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	if err := service.UpdateMapping(otherTenantID, account.ID, mapping.ID, ChannelMappingUpdate{ExternalCode: "FORGED", Status: "active"}); err == nil {
		t.Fatal("another tenant updated the mapping")
	}
	if err := service.UpdateMapping(tenantID, account.ID, mapping.ID, ChannelMappingUpdate{ExternalCode: "XHS-NEW", Status: "unknown"}); err == nil {
		t.Fatal("invalid mapping status was accepted")
	}
}

func TestXiaohongshuProductConfigAndSyncAreTenantScoped(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-product", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-product", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-PRODUCT", DisplayName: "沙盒门票", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/apps/category":
			_, _ = w.Write([]byte(`{"data":{"category_info":[{"category_id":"SCENIC","name":"景区门票","support_trade":true}]},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/poi/list":
			_, _ = w.Write([]byte(`{"data":{"list":[{"poi_id":"POI-1","name":"测试景区"}],"total":1},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/poi/product/upsert":
			var request xiaohongshu.LocalLifeProductRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ExternalProductID != "XHS-PRODUCT" || request.CategoryID != "SCENIC" || len(request.SKUs) != 1 || request.SKUs[0].ExternalSKUID != "XHS-SKU" || request.SKUs[0].SalePrice != 1 {
				t.Fatalf("request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := NewXiaohongshuProductService()
	service.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		if appID != "miniapp-product" || secret != "app-secret" || environment != "sandbox" {
			t.Fatalf("client app=%q secret=%q environment=%q", appID, secret, environment)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	input := XiaohongshuProductConfigInput{ExternalSKUID: "XHS-SKU", CategoryID: "SCENIC", POIIDs: []string{"POI-1"}, ImageURL: "https://example.com/ticket.png", Description: "测试景区门票", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 2}
	config, err := service.SaveConfig(tenantID, account.ID, mapping.ID, 1, "admin", input)
	if err != nil || config.SyncStatus != "pending" || len(config.POIIDs) != 1 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	categories, err := service.ListCategories(context.Background(), tenantID, account.ID)
	if err != nil || len(categories) != 1 || categories[0].ID != "SCENIC" {
		t.Fatalf("categories=%+v err=%v", categories, err)
	}
	pois, err := service.ListPOIs(context.Background(), tenantID, account.ID, 1, 20)
	if err != nil || pois.Total != 1 || pois.List[0].ID != "POI-1" {
		t.Fatalf("pois=%+v err=%v", pois, err)
	}
	if err := service.Sync(context.Background(), tenantID, account.ID, mapping.ID, 1, "admin"); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetConfig(tenantID, account.ID, mapping.ID)
	if err != nil || stored.SyncStatus != "synced" || stored.LastSyncedAt == nil {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	if _, err := service.SaveConfig(otherTenantID, account.ID, mapping.ID, 1, "admin", input); err == nil {
		t.Fatal("another tenant configured the mapping")
	}
}

func TestXiaohongshuDiagnosisChecksCredentialsTradeCategoriesAndPOIs(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-diagnosis", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, "miniapp-diagnosis", "app-secret", "MessageToken123", "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/apps/category":
			_, _ = w.Write([]byte(`{"data":{"category_info":[{"category_id":"SCENIC","name":"景区门票","support_trade":true},{"category_id":"OTHER","name":"其他","support_trade":false}]},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/poi/list":
			if r.URL.Query().Get("page_no") != "1" || r.URL.Query().Get("page_size") != "100" {
				t.Fatalf("poi query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"list":[{"poi_id":"POI-1","name":"测试景区"}],"total":1},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	productService := NewXiaohongshuProductService()
	productService.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		if appID != "miniapp-diagnosis" || secret != "app-secret" || environment != "sandbox" {
			t.Fatalf("client app=%q secret=%q environment=%q", appID, secret, environment)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	diagnostic, err := productService.Diagnose(context.Background(), tenantID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnostic.Ready || diagnostic.Credentials.Status != "passed" || diagnostic.Categories.Count != 2 || diagnostic.Categories.TradeCount != 1 || diagnostic.POIs.Count != 1 {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
}

func TestXiaohongshuDiagnosisFailsClosedForMissingTradeCategory(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-diagnosis-no-trade", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-diagnosis-no-trade", "app-secret"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/apps/category":
			_, _ = w.Write([]byte(`{"data":{"category_info":[{"category_id":"OTHER","name":"其他","support_trade":false}]},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/poi/list":
			_, _ = w.Write([]byte(`{"data":{"list":[{"poi_id":"POI-1","name":"测试景区"}],"total":1},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	productService := NewXiaohongshuProductService()
	productService.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	diagnostic, err := productService.Diagnose(context.Background(), tenantID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Ready || diagnostic.Categories.Status != "passed" || diagnostic.Categories.TradeCount != 0 {
		t.Fatalf("diagnostic should not be ready=%+v", diagnostic)
	}
}
