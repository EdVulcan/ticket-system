package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func TestXiaohongshuProductAuditProductionTimeField(t *testing.T) {
	payload := []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"XHS_TEST_MULTI_20260911","Status":2,"AuditTime":1789111852,"RejectReason":"无意义商品"}`)
	product, status, reason, at := parseXiaohongshuProductAudit(payload)
	if product != "XHS_TEST_MULTI_20260911" || status != "rejected" || reason != "无意义商品" || at == nil || at.Unix() != 1789111852 {
		t.Fatalf("production callback lost rejection: product=%q status=%q reason=%q time=%v", product, status, reason, at)
	}
}

func TestXiaohongshuProductAuditRestoresInboxReasonWithoutCrossingSubmission(t *testing.T) {
	resetBusinessData(t)
	tenantID, accountID, mappingID, service := seedXiaohongshuAuditTarget(t, "REJECT", 0)
	otherTenantID, otherAccountID, _, _ := seedXiaohongshuAuditTarget(t, "REJECT", 0)
	submitted := time.Unix(1789111739, 0)
	latest := int64(1789111852)
	update := func(values map[string]interface{}) {
		t.Helper()
		if err := model.DB.Model(&model.XiaohongshuProductConfig{}).Where("channel_product_mapping_id = ?", mappingID).Updates(values).Error; err != nil {
			t.Fatal(err)
		}
	}
	update(map[string]interface{}{"last_synced_at": submitted, "audit_status": "rejected"})
	addEvent := func(tenant, account uint, product, reason string, audited int64) {
		t.Helper()
		payload, err := json.Marshal(map[string]interface{}{"Event": "PRODUCT_AUDIT", "OutProductId": product, "Status": 2, "AuditTime": audited, "RejectReason": reason})
		if err != nil {
			t.Fatal(err)
		}
		ciphertext, err := utils.EncryptAES(string(payload))
		if err != nil {
			t.Fatal(err)
		}
		event := model.XiaohongshuWebhookEvent{TenantID: tenant, ChannelAccountID: account, EventType: "PRODUCT_AUDIT", PayloadHash: reason, PayloadCiphertext: ciphertext, Status: "manual_review", LastError: xiaohongshuProductAuditUnrecognizedReason, ReceivedAt: submitted.Add(5 * time.Minute)}
		if err := model.DB.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	addEvent(tenantID, accountID, "AUDIT-PRODUCT", "earlier review", latest-32)
	addEvent(tenantID, accountID, "AUDIT-PRODUCT", "无意义商品", latest)
	addEvent(tenantID, accountID, "OTHER-PRODUCT", "wrong product", latest+1)
	addEvent(otherTenantID, otherAccountID, "AUDIT-PRODUCT", "wrong tenant", latest+2)
	addEvent(tenantID, accountID, "AUDIT-PRODUCT", "previous submission", submitted.Unix()-1)
	var queryTime int64
	original := service.Products.NewClient
	service.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		client := original(appID, secret, environment)
		client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/api/rmp/token" {
				return jsonResponse(`{"data":{"access_token":"token","expire_in":7200},"success":true,"code":0}`), nil
			}
			data := map[string]interface{}{"out_product_id": "AUDIT-PRODUCT", "audit_status": "REJECT"}
			if queryTime != 0 {
				data["audit_info"] = map[string]interface{}{"audit_time": queryTime}
			}
			body, _ := json.Marshal(map[string]interface{}{"data": data, "success": true, "code": 0})
			return jsonResponse(string(body)), nil
		})}
		return client
	}
	check := func(reason string, audited int64) {
		t.Helper()
		view, err := service.RefreshAudit(context.Background(), tenantID, accountID, mappingID)
		if err != nil || view == nil || view.AuditStatus != "rejected" || view.AuditMessage != reason {
			t.Fatalf("view=%+v err=%v want reason=%q", view, err, reason)
		}
		if (audited == 0 && view.AuditedAt != nil) || (audited != 0 && (view.AuditedAt == nil || view.AuditedAt.Unix() != audited)) {
			t.Fatalf("audit time=%v want=%d", view.AuditedAt, audited)
		}
	}
	check("无意义商品", latest) // Old manual-review callback is recovered.
	check("无意义商品", latest) // A second empty query must not erase it.
	var alteredEvents int64
	if err := model.DB.Model(&model.XiaohongshuWebhookEvent{}).Where("status <> ? OR last_error <> ?", "manual_review", xiaohongshuProductAuditUnrecognizedReason).Count(&alteredEvents).Error; err != nil || alteredEvents != 0 {
		t.Fatalf("inbox history changed: count=%d err=%v", alteredEvents, err)
	}
	queryTime = latest + 60
	check("", queryTime) // A different review cannot inherit the old reason.
	queryTime = submitted.Unix() - 1
	check("", 0) // A stale provider time must not be persisted.
	queryTime = 0
	update(map[string]interface{}{"last_synced_at": submitted.Add(time.Hour), "audit_status": "pending", "audit_message": "", "audited_at": nil})
	check("", 0) // Resubmission cannot reuse old callbacks.
	update(map[string]interface{}{"last_synced_at": nil, "audit_status": "rejected", "audit_message": "unproven old reason", "audited_at": time.Unix(latest, 0)})
	check("", 0) // No submission timestamp means no recovery from stored data.
}
