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
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	const appID = "xhs-webhook-app"
	const token = "WebhookToken123"
	const encodingAESKey = "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	account := model.ChannelAccount{Code: "xiaohongshu-webhook"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, appID, "app-secret", token, encodingAESKey); err != nil {
		t.Fatal(err)
	}

	webhook := XiaohongshuWebhookService{}
	signature := xiaohongshu.MessageSignature(token, "1700000000", "verify-nonce")
	echo, err := webhook.VerifyURL(appID, signature, "1700000000", "verify-nonce", "verify-echo")
	if err != nil || echo != "verify-echo" {
		t.Fatalf("echo=%q err=%v", echo, err)
	}

	payload := []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"P1","Status":2}`)
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
	if len(events) != 1 || events[0].TenantID != tenantID || events[0].ChannelAccountID != account.ID || events[0].EventType != "PRODUCT_AUDIT" || events[0].Status != "pending" || events[0].LastError != "" {
		t.Fatalf("events=%+v", events)
	}
	storedPayload, err := utils.DecryptAES(events[0].PayloadCiphertext)
	if err != nil || storedPayload != string(payload) {
		t.Fatalf("payload=%q err=%v", storedPayload, err)
	}

	message.MsgSignature = "invalid"
	if err := webhook.Receive(context.Background(), appID, message); err != ErrXiaohongshuWebhookSignature {
		t.Fatalf("invalid signature error=%v", err)
	}

	afterSalePayload := []byte(`{"Event":"AFTER_SALE_REFUND","OrderId":"external-order","Status":"SUCCESS"}`)
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
