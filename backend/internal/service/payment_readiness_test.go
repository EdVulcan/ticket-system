package service

import (
	"testing"
	"ticket-backend/internal/model"
)

func TestPaymentConfigIssuesRequiresProviderCallbackFacts(t *testing.T) {
	wechat := model.PaymentConfig{
		TenantID: 7, Provider: "wechat", AppID: "wx-app", MchID: "merchant",
		Key: "12345678901234567890123456789012", SerialNo: "serial", PrivateKey: "private",
		PlatformPublicKeyID: "platform-key-id", PlatformPublicKey: "platform-public",
		NotifyURL: "https://tickets.example.com/api/v1/payments/notify/wechat/7", Status: true,
	}
	if issues := PaymentConfigIssues(&wechat, 7); len(issues) != 0 {
		t.Fatalf("complete WeChat config issues=%v", issues)
	}

	alipay := model.PaymentConfig{
		TenantID: 7, Provider: "alipay", AppID: "ali-app", PrivateKey: "private", PublicKey: "public",
		NotifyURL: "https://tickets.example.com/api/v1/payments/notify/alipay/7", Status: true,
	}
	if issues := PaymentConfigIssues(&alipay, 7); len(issues) != 0 {
		t.Fatalf("complete Alipay config issues=%v", issues)
	}
}

func TestPaymentConfigIssuesRejectsUnsafeOrWrongTenantCallback(t *testing.T) {
	cfg := model.PaymentConfig{
		TenantID: 7, Provider: "alipay", AppID: "ali-app", PrivateKey: "private", PublicKey: "public",
		NotifyURL: "http://tickets.example.com/api/v1/payments/notify/alipay/8", Status: true,
	}
	if issues := PaymentConfigIssues(&cfg, 7); len(issues) < 1 {
		t.Fatal("unsafe wrong-tenant callback was accepted")
	}
}
