package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
)

func TestXiaohongshuWebhookVerifiesDecryptsAndPersistsIdempotently(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	const appID = "xhs-webhook-app"
	const token = "WebhookToken123"
	const encodingAESKey = "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	account := model.ChannelAccount{Code: "xiaohongshu-webhook"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, appID, "app-secret", token, encodingAESKey); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "P1", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "P1-SKU", CategoryID: "ticket", ImageURL: "https://example.com/p1.png", Description: "P1",
		ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1,
		SyncStatus: "submitted", AuditStatus: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}

	webhook := XiaohongshuWebhookService{}
	signature := xiaohongshu.MessageSignature(token, "1700000000", "verify-nonce")
	echo, err := webhook.VerifyURL(appID, signature, "1700000000", "verify-nonce", "verify-echo")
	if err != nil || echo != "verify-echo" {
		t.Fatalf("echo=%q err=%v", echo, err)
	}

	payload := []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"P1","Status":"approved"}`)
	encrypted := encryptXiaohongshuWebhookFixture(t, encodingAESKey, payload, appID)
	message := XiaohongshuWebhookMessage{
		Nonce: "event-nonce", Timestamp: 1700000001, Encrypt: encrypted,
		MsgSignature: xiaohongshu.MessageSignature(token, strconv.FormatInt(1700000001, 10), "event-nonce", encrypted),
	}
	if err := webhook.Receive(context.Background(), appID, message); err != nil {
		t.Fatal(err)
	}
	if err := webhook.Receive(context.Background(), appID, message); err != nil {
		t.Fatal(err)
	}

	var events []model.XiaohongshuWebhookEvent
	if err := model.DB.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TenantID != tenantID || events[0].ChannelAccountID != account.ID || events[0].EventType != "PRODUCT_AUDIT" || events[0].Status != "processed" || events[0].LastError != "" || events[0].ProcessedAt == nil {
		t.Fatalf("events=%+v", events)
	}
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("channel_product_mapping_id = ? AND tenant_id = ?", mapping.ID, tenantID).First(&config).Error; err != nil {
		t.Fatal(err)
	}
	if config.AuditStatus != "approved" || config.AuditedAt == nil {
		t.Fatalf("config=%+v", config)
	}
	storedPayload, err := utils.DecryptAES(events[0].PayloadCiphertext)
	if err != nil || storedPayload != string(payload) {
		t.Fatalf("payload=%q err=%v", storedPayload, err)
	}

	message.MsgSignature = "invalid"
	if err := webhook.Receive(context.Background(), appID, message); err != ErrXiaohongshuWebhookSignature {
		t.Fatalf("invalid signature error=%v", err)
	}

	afterSalePayload := []byte(`{"Event":"AFTER_SALE_REFUND","OrderId":"external-order","AfterSaleId":"after-sale-1","RefundId":"refund-1","Status":"SUCCESS"}`)
	afterSaleEncrypted := encryptXiaohongshuWebhookFixture(t, encodingAESKey, afterSalePayload, appID)
	afterSaleMessage := XiaohongshuWebhookMessage{
		Nonce: "after-sale-nonce", Timestamp: 1700000002, Encrypt: afterSaleEncrypted,
		MsgSignature: xiaohongshu.MessageSignature(token, strconv.FormatInt(1700000002, 10), "after-sale-nonce", afterSaleEncrypted),
	}
	if err := webhook.Receive(context.Background(), appID, afterSaleMessage); err != nil {
		t.Fatal(err)
	}
	var afterSaleEvent model.XiaohongshuWebhookEvent
	if err := model.DB.Where("event_type = ?", "AFTER_SALE_REFUND").First(&afterSaleEvent).Error; err != nil {
		t.Fatal(err)
	}
	if afterSaleEvent.Status != "manual_review" || afterSaleEvent.LastError != xiaohongshuWebhookManualReviewReason || afterSaleEvent.ProcessedAt != nil {
		t.Fatalf("after-sale webhook was not fail-closed: %+v", afterSaleEvent)
	}
	var coordination model.XiaohongshuRefundCoordination
	if err := model.DB.Where("webhook_event_id = ?", afterSaleEvent.ID).First(&coordination).Error; err != nil {
		t.Fatalf("after-sale coordination was not created: %v", err)
	}
	if coordination.TenantID != tenantID || coordination.ChannelAccountID != account.ID || coordination.Scope != "account" || coordination.State != "received_unmapped" || coordination.ExternalOrderID != "external-order" || coordination.ExternalAfterSaleID != "after-sale-1" || coordination.ExternalRefundID != "refund-1" {
		t.Fatalf("unexpected after-sale coordination=%+v", coordination)
	}
	// A retry with a different payload but the same provider after-sale number
	// must remain one business hold; only the webhook inbox gets a second event.
	secondAfterSalePayload := []byte(`{"Event":"AFTER_SALE_REFUND","OrderId":"external-order","AfterSaleId":"after-sale-1","RefundId":"refund-1","Status":"PROCESSING"}`)
	secondAfterSaleEncrypted := encryptXiaohongshuWebhookFixture(t, encodingAESKey, secondAfterSalePayload, appID)
	secondAfterSaleMessage := XiaohongshuWebhookMessage{
		Nonce: "after-sale-nonce-2", Timestamp: 1700000004, Encrypt: secondAfterSaleEncrypted,
		MsgSignature: xiaohongshu.MessageSignature(token, "1700000004", "after-sale-nonce-2", secondAfterSaleEncrypted),
	}
	if err := webhook.Receive(context.Background(), appID, secondAfterSaleMessage); err != nil {
		t.Fatal(err)
	}
	var coordinationCount int64
	if err := model.DB.Model(&model.XiaohongshuRefundCoordination{}).Where("tenant_id = ? AND channel_account_id = ? AND external_after_sale_id = ?", tenantID, account.ID, "after-sale-1").Count(&coordinationCount).Error; err != nil {
		t.Fatal(err)
	}
	if coordinationCount != 1 {
		t.Fatalf("same after-sale created %d coordination records", coordinationCount)
	}

	unknownPayload := []byte(`{"Event":"UNDOCUMENTED_BUSINESS_EVENT","OrderId":"external-order"}`)
	unknownEncrypted := encryptXiaohongshuWebhookFixture(t, encodingAESKey, unknownPayload, appID)
	unknownMessage := XiaohongshuWebhookMessage{
		Nonce: "unknown-event-nonce", Timestamp: 1700000003, Encrypt: unknownEncrypted,
		MsgSignature: xiaohongshu.MessageSignature(token, strconv.FormatInt(1700000003, 10), "unknown-event-nonce", unknownEncrypted),
	}
	if err := webhook.Receive(context.Background(), appID, unknownMessage); err != nil {
		t.Fatal(err)
	}
	var unknownEvent model.XiaohongshuWebhookEvent
	if err := model.DB.Where("event_type = ?", "UNDOCUMENTED_BUSINESS_EVENT").First(&unknownEvent).Error; err != nil {
		t.Fatal(err)
	}
	if unknownEvent.Status != "manual_review" || unknownEvent.LastError != xiaohongshuWebhookManualReviewReason {
		t.Fatalf("unknown webhook was not fail-closed: %+v", unknownEvent)
	}
}

func TestXiaohongshuProductAuditWebhookFailsClosedForRejectedOfflineAndUnknownStatus(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	const appID = "xhs-product-audit"
	const token = "ProductAuditToken123"
	const encodingAESKey = "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	account := model.ChannelAccount{Code: "xiaohongshu-product-audit"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, appID, "app-secret", token, encodingAESKey); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "AUDIT-PRODUCT", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	config := model.XiaohongshuProductConfig{
		TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "AUDIT-SKU", CategoryID: "ticket", ImageURL: "https://example.com/audit.png", Description: "audit",
		ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1,
		SyncStatus: "submitted", AuditStatus: "approved",
	}
	if err := model.DB.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	otherTenantID, otherProductID := seedSellableProduct(t, "unlimited", 0)
	otherAccount := model.ChannelAccount{Code: "xiaohongshu-product-audit-other"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(otherTenantID, &otherAccount, "xhs-product-audit-other", "other-secret", "OtherAuditToken123", encodingAESKey); err != nil {
		t.Fatal(err)
	}
	otherMapping := model.ChannelProductMapping{ChannelAccountID: otherAccount.ID, ProductID: otherProductID, ExternalCode: "AUDIT-PRODUCT", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(otherTenantID, &otherMapping); err != nil {
		t.Fatal(err)
	}
	otherConfig := model.XiaohongshuProductConfig{
		TenantID: otherTenantID, ChannelAccountID: otherAccount.ID, ChannelProductMappingID: otherMapping.ID,
		ExternalSKUID: "OTHER-SKU", CategoryID: "ticket", ImageURL: "https://example.com/other.png", Description: "other",
		ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1,
		SyncStatus: "submitted", AuditStatus: "approved",
	}
	if err := model.DB.Create(&otherConfig).Error; err != nil {
		t.Fatal(err)
	}
	webhook := XiaohongshuWebhookService{}
	receive := func(nonce string, payload []byte) {
		t.Helper()
		encrypted := encryptXiaohongshuWebhookFixture(t, encodingAESKey, payload, appID)
		message := XiaohongshuWebhookMessage{Nonce: nonce, Timestamp: 1700000010, Encrypt: encrypted, MsgSignature: xiaohongshu.MessageSignature(token, "1700000010", nonce, encrypted)}
		if err := webhook.Receive(context.Background(), appID, message); err != nil {
			t.Fatal(err)
		}
	}
	receive("rejected", []byte(`{"Event":"PRODUCT_AUDIT","out_product_id":"AUDIT-PRODUCT","audit_status":"rejected","reject_reason":"missing document"}`))
	if err := model.DB.First(&config, config.ID).Error; err != nil || config.AuditStatus != "rejected" || config.AuditMessage != "missing document" {
		t.Fatalf("rejected config=%+v err=%v", config, err)
	}
	if err := model.DB.First(&otherConfig, otherConfig.ID).Error; err != nil || otherConfig.AuditStatus != "approved" {
		t.Fatalf("cross-tenant config=%+v err=%v", otherConfig, err)
	}
	receive("offline", []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"AUDIT-PRODUCT","Status":"offline","Message":"off shelf"}`))
	if err := model.DB.First(&config, config.ID).Error; err != nil || config.AuditStatus != "offline" || config.AuditMessage != "off shelf" {
		t.Fatalf("offline config=%+v err=%v", config, err)
	}
	receive("pending", []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"AUDIT-PRODUCT","Status":"auditing","Message":"under review"}`))
	var pendingConfig model.XiaohongshuProductConfig
	if err := model.DB.First(&pendingConfig, config.ID).Error; err != nil || pendingConfig.AuditStatus != "pending" || pendingConfig.AuditMessage != "under review" || pendingConfig.AuditedAt != nil {
		t.Fatalf("pending audit should not set audited_at: config=%+v err=%v", pendingConfig, err)
	}
	receive("numeric", []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"AUDIT-PRODUCT","Status":2}`))
	if err := model.DB.First(&config, config.ID).Error; err != nil || config.AuditStatus != "pending" || config.AuditMessage != xiaohongshuProductAuditUnrecognizedReason {
		t.Fatalf("numeric audit status was accepted: config=%+v err=%v", config, err)
	}
	receive("unknown", []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"AUDIT-PRODUCT","Status":99}`))
	if err := model.DB.First(&config, config.ID).Error; err != nil || config.AuditStatus != "pending" || config.AuditMessage != xiaohongshuProductAuditUnrecognizedReason {
		t.Fatalf("unknown config=%+v err=%v", config, err)
	}
	var unknownEvent model.XiaohongshuWebhookEvent
	if err := model.DB.Where("event_type = ? AND status = ?", "PRODUCT_AUDIT", "manual_review").First(&unknownEvent).Error; err != nil || unknownEvent.LastError != xiaohongshuProductAuditUnrecognizedReason {
		t.Fatalf("unknown event=%+v err=%v", unknownEvent, err)
	}
}

func encryptXiaohongshuWebhookFixture(t *testing.T, encodingAESKey string, payload []byte, appID string) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := make([]byte, 20+len(payload)+len(appID))
	copy(plaintext[:16], []byte("0123456789abcdef"))
	binary.BigEndian.PutUint32(plaintext[16:20], uint32(len(payload)))
	copy(plaintext[20:], payload)
	copy(plaintext[20+len(payload):], appID)
	padding := 32 - len(plaintext)%32
	for range padding {
		plaintext = append(plaintext, byte(padding))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(ciphertext)
}
