package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
)

func TestCommercePaymentNotifyURLUsesSystemOwnedPaths(t *testing.T) {
	oldBaseURL := config.GlobalConfig.Server.PublicBaseURL
	config.GlobalConfig.Server.PublicBaseURL = "https://tickets.example.com/"
	t.Cleanup(func() { config.GlobalConfig.Server.PublicBaseURL = oldBaseURL })

	paymentURL, err := CommercePaymentNotifyURL("payments", 7)
	if err != nil {
		t.Fatalf("payment callback URL: %v", err)
	}
	refundURL, err := CommercePaymentNotifyURL("refunds", 7)
	if err != nil {
		t.Fatalf("refund callback URL: %v", err)
	}
	if paymentURL != "https://tickets.example.com/api/v1/commerce/payments/notify/wechat/7" || refundURL != "https://tickets.example.com/api/v1/commerce/refunds/notify/wechat/7" {
		t.Fatalf("unexpected commerce callback URLs: payment=%q refund=%q", paymentURL, refundURL)
	}
	if _, err := CommercePaymentNotifyURL("unknown", 7); err == nil {
		t.Fatal("unknown commerce callback kind was accepted")
	}
}

func TestCommercePaymentNotifyURLRejectsUnsafeBaseURL(t *testing.T) {
	oldBaseURL := config.GlobalConfig.Server.PublicBaseURL
	config.GlobalConfig.Server.PublicBaseURL = "http://tickets.example.com"
	t.Cleanup(func() { config.GlobalConfig.Server.PublicBaseURL = oldBaseURL })
	if _, err := CommercePaymentNotifyURL("payments", 7); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("unsafe base URL was accepted: %v", err)
	}
}

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

func TestWechatPaymentCodeCapabilityRequiresV2KeyAndCertificate(t *testing.T) {
	cfg := &model.PaymentConfig{WechatV2Key: "12345678901234567890123456789012", MerchantCertificate: "certificate"}
	capabilities := paymentCapabilities("wechat", true, true, cfg)
	if !capabilities[1].Available {
		t.Fatalf("complete payment-code credentials should be ready: %+v", capabilities[1])
	}
	cfg.MerchantCertificate = ""
	capabilities = paymentCapabilities("wechat", true, true, cfg)
	if capabilities[1].Available || capabilities[1].Note != "请重新上传商户证书和私钥" {
		t.Fatalf("missing merchant certificate was not reported: %+v", capabilities[1])
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
