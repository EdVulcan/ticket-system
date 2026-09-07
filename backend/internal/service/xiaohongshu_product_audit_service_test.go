package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func TestXiaohongshuProductAuditRefreshReconcilesOfficialStates(t *testing.T) {
	for _, test := range []struct {
		name, upstream, wantStatus, wantMessage string
		wantAudited                             bool
		wantErr                                 error
	}{
		{"pass", "PASS", "approved", "", true, nil},
		{"reject", "REJECT", "rejected", "missing document", true, nil},
		{"auditing", "AUDITING", "pending", "", false, nil},
		{"unknown", "UNKNOWN", "pending", "小红书审核状态无法识别，商品保持不可售", false, ErrXiaohongshuProductAuditState},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetBusinessData(t)
			tenantID, accountID, mappingID, service := seedXiaohongshuAuditTarget(t, test.upstream, 0)
			view, err := service.RefreshAudit(context.Background(), tenantID, accountID, mappingID)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v want=%v", err, test.wantErr)
			}
			if view == nil || view.AuditStatus != test.wantStatus || view.AuditMessage != test.wantMessage || (view.AuditedAt != nil) != test.wantAudited {
				t.Fatalf("view=%+v", view)
			}
			if test.wantErr != nil && view.AuditCheckError == "" {
				t.Fatalf("unknown status was not recorded for reconciliation: %+v", view)
			}
		})
	}
}

func TestXiaohongshuProductAuditRefreshRejectsTenantMismatchBeforeUpstream(t *testing.T) {
	resetBusinessData(t)
	tenantID, accountID, mappingID, service := seedXiaohongshuAuditTarget(t, "PASS", 0)
	var calls atomic.Int32
	service.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		calls.Add(1)
		return nil
	}
	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	if _, err := service.RefreshAudit(context.Background(), otherTenantID, accountID, mappingID); err == nil {
		t.Fatal("cross-tenant refresh was accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("cross-tenant refresh called upstream %d times", calls.Load())
	}
	_ = tenantID
}

func TestXiaohongshuProductAuditRefreshFailureAndStaleResponseDoNotApprove(t *testing.T) {
	resetBusinessData(t)
	tenantID, accountID, mappingID, service := seedXiaohongshuAuditTarget(t, "PASS", 500)
	if _, err := service.RefreshAudit(context.Background(), tenantID, accountID, mappingID); err == nil || !strings.Contains(err.Error(), "查询失败") {
		t.Fatalf("provider failure error=%v", err)
	}
	var failed model.XiaohongshuProductConfig
	if err := model.DB.Where("channel_product_mapping_id = ?", mappingID).First(&failed).Error; err != nil || failed.AuditStatus != "pending" || failed.AuditCheckError == "" {
		t.Fatalf("failed audit=%+v err=%v", failed, err)
	}

	resetBusinessData(t)
	tenantID, accountID, mappingID, service = seedXiaohongshuAuditTarget(t, "PASS", 0)
	started := make(chan struct{})
	release := make(chan struct{})
	original := service.Products.NewClient
	service.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		client := original(appID, secret, environment)
		client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/api/rmp/token" {
				return jsonResponse(`{"data":{"access_token":"token","expire_in":7200},"success":true,"code":0}`), nil
			}
			close(started)
			<-release
			return jsonResponse(`{"data":{"out_product_id":"AUDIT-PRODUCT","audit_status":"PASS","audit_info":{"audit_time":1700000000}},"success":true,"code":0}`), nil
		})}
		return client
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.RefreshAudit(context.Background(), tenantID, accountID, mappingID)
		done <- err
	}()
	<-started
	if err := model.DB.Model(&model.XiaohongshuProductConfig{}).Where("channel_product_mapping_id = ?", mappingID).Updates(map[string]interface{}{"audit_status": "rejected", "audit_message": "newer callback"}).Error; err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var current model.XiaohongshuProductConfig
	if err := model.DB.Where("channel_product_mapping_id = ?", mappingID).First(&current).Error; err != nil || current.AuditStatus != "rejected" {
		t.Fatalf("stale PASS overwrote newer callback: %+v err=%v", current, err)
	}
}

func TestXiaohongshuProductAuditSchedulerUsesPersistedIntervalAndContinuesAfterFailure(t *testing.T) {
	resetBusinessData(t)
	firstTenantID, firstAccountID, firstMappingID, first := seedXiaohongshuAuditTarget(t, "PASS", 500)
	secondTenantID, secondAccountID, secondMappingID, second := seedXiaohongshuAuditTarget(t, "PASS", 0)
	var firstAccount, secondAccount model.ChannelAccount
	if err := model.DB.First(&firstAccount, firstAccountID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&secondAccount, secondAccountID).Error; err != nil {
		t.Fatal(err)
	}
	firstFactory, secondFactory := first.Products.NewClient, second.Products.NewClient
	clock := time.Unix(1700001000, 0)
	service := first
	service.Now = func() time.Time { return clock }
	service.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		switch appID {
		case firstAccount.AppID:
			return firstFactory(appID, secret, environment)
		case secondAccount.AppID:
			return secondFactory(appID, secret, environment)
		default:
			t.Fatalf("unexpected app id %q", appID)
			return nil
		}
	}
	processed, err := service.ProcessProductAuditRefreshes(context.Background(), 2)
	if processed != 2 || err == nil {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	var failed, approved model.XiaohongshuProductConfig
	if err := model.DB.Where("channel_product_mapping_id = ?", firstMappingID).First(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("channel_product_mapping_id = ?", secondMappingID).First(&approved).Error; err != nil {
		t.Fatal(err)
	}
	if failed.AuditCheckError == "" || approved.AuditStatus != "approved" || approved.AuditCheckedAt == nil {
		t.Fatalf("failed=%+v approved=%+v", failed, approved)
	}
	processed, err = service.ProcessProductAuditRefreshes(context.Background(), 2)
	if processed != 0 || err != nil {
		t.Fatalf("recent checks were not deferred: processed=%d err=%v", processed, err)
	}
	if err := model.DB.Model(&model.XiaohongshuProductConfig{}).Where("channel_product_mapping_id = ?", firstMappingID).Update("audit_checked_at", clock.Add(-xiaohongshuProductAuditInterval)).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = service.ProcessProductAuditRefreshes(context.Background(), 1)
	if processed != 1 || err == nil {
		t.Fatalf("due failed mapping did not run independently: processed=%d err=%v", processed, err)
	}
	_ = firstTenantID
	_ = secondTenantID
}

func seedXiaohongshuAuditTarget(t *testing.T, status string, providerCode int) (uint, uint, uint, XiaohongshuProductAuditService) {
	t.Helper()
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xhs-audit-" + time.Now().Format("150405.000000000")}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "xhs-audit-app-"+time.Now().Format("150405.000000000"), "audit-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "AUDIT-PRODUCT", Status: "active", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "AUDIT-SKU", CategoryID: "ticket", ImageURL: "https://example.com/audit.png", Description: "audit", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "submitted", AuditStatus: "pending"}).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/rmp/token" {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","expire_in":7200},"success":true,"code":0}`))
			return
		}
		if request.URL.Path != "/api/rmp/mp/deal/product/get" {
			http.NotFound(w, request)
			return
		}
		if providerCode != 0 {
			_, _ = w.Write([]byte(`{"success":false,"code":12345,"msg":"provider failure"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"out_product_id":"AUDIT-PRODUCT","audit_status":"` + status + `","audit_info":{"audit_time":1700000000,"reject_reason":"missing document"}},"success":true,"code":0}`))
	}))
	t.Cleanup(server.Close)
	service := NewXiaohongshuProductAuditService()
	service.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	return tenantID, account.ID, mapping.ID, service
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
