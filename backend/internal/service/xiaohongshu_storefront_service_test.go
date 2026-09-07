package service

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"ticket-backend/internal/model"
)

func storefrontPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	if err := png.Encode(&data, canvas); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestXiaohongshuStorefrontIsAccountScopedAndAudited(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	first := model.ChannelAccount{Code: "storefront-first"}
	second := model.ChannelAccount{Code: "storefront-second"}
	channels := ChannelService{}
	if err := channels.CreateXiaohongshu(tenantID, &first, "storefront-first-app", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateXiaohongshu(tenantID, &second, "storefront-second-app", "secret"); err != nil {
		t.Fatal(err)
	}
	store := XiaohongshuImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	service := XiaohongshuStorefrontService{Images: store}
	firstURL, err := service.Upload(tenantID, first.ID, storefrontPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(tenantID, first.ID, 42, "admin", firstURL); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(tenantID, first.ID)
	if err != nil || got != firstURL {
		t.Fatalf("storefront image=%q err=%v", got, err)
	}
	var audits int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ? AND target_id = ?", tenantID, "xiaohongshu.storefront.image", first.ID).Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	secondURL, err := service.Upload(tenantID, second.ID, storefrontPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(tenantID, first.ID, 42, "admin", secondURL); err == nil || !strings.Contains(err.Error(), "当前渠道账号") {
		t.Fatalf("same-tenant account image was accepted: %v", err)
	}
	if _, err := service.Set(tenantID, first.ID, 42, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Get(tenantID, first.ID); err != nil || got != "" {
		t.Fatalf("cleared storefront image=%q err=%v", got, err)
	}
}

func TestXiaohongshuStorefrontRejectsOtherTenantUnsupportedChannelAndInjectedCreateField(t *testing.T) {
	resetBusinessData(t)
	firstTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	secondTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	channels := ChannelService{}
	first := model.ChannelAccount{Code: "storefront-owner", StorefrontImageURL: "https://attacker.example.com/injected.png"}
	if err := channels.CreateXiaohongshu(firstTenantID, &first, "storefront-owner-app", "secret"); err != nil {
		t.Fatal(err)
	}
	var persisted model.ChannelAccount
	if err := model.DB.First(&persisted, first.ID).Error; err != nil || persisted.StorefrontImageURL != "" {
		t.Fatalf("create accepted injected storefront image: %+v err=%v", persisted, err)
	}
	other := model.ChannelAccount{Code: "storefront-other"}
	if err := channels.CreateXiaohongshu(secondTenantID, &other, "storefront-other-app", "secret"); err != nil {
		t.Fatal(err)
	}
	store := XiaohongshuImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	service := XiaohongshuStorefrontService{Images: store}
	otherURL, err := service.Upload(secondTenantID, other.ID, storefrontPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(firstTenantID, other.ID); err == nil {
		t.Fatal("cross-tenant storefront read was accepted")
	}
	if _, err := service.Upload(firstTenantID, other.ID, storefrontPNG(t)); err == nil {
		t.Fatal("cross-tenant storefront upload was accepted")
	}
	if _, err := service.Set(firstTenantID, other.ID, 42, "admin", otherURL); err == nil {
		t.Fatal("cross-tenant storefront write was accepted")
	}
	if _, err := service.Set(firstTenantID, first.ID, 42, "admin", otherURL); err == nil {
		t.Fatal("cross-tenant managed image was accepted")
	}
	ctrip := model.ChannelAccount{Code: "storefront-ctrip"}
	if err := channels.CreateCtrip(firstTenantID, &ctrip, "ctrip-storefront", "sign-key", "1234567890abcdef", "abcdef1234567890"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(firstTenantID, ctrip.ID); err == nil {
		t.Fatal("unsupported channel storefront was accepted")
	}
	if _, err := service.Upload(firstTenantID, ctrip.ID, storefrontPNG(t)); err == nil {
		t.Fatal("unsupported channel storefront upload was accepted")
	}
}
