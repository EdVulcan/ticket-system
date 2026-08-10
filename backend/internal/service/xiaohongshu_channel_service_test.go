package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
)

func TestXiaohongshuChannelCredentialsAreTenantScopedAndEncrypted(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-main", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-1", "app-secret-1"); err != nil {
		t.Fatal(err)
	}
	if account.Type != "xiaohongshu" || account.Status != "active" || account.Environment != "production" {
		t.Fatalf("account=%+v", account)
	}
	if account.SecretCiphertext == "" || strings.Contains(account.SecretCiphertext, "app-secret-1") {
		t.Fatal("xiaohongshu app secret was not encrypted")
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
	if err := (&ChannelService{}).ConfigureXiaohongshu(otherTenantID, account.ID, "miniapp-forged", "forged-secret"); err == nil {
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
	if err := service.CreateXiaohongshu(tenantID, &account, "miniapp-old", "old-secret"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureXiaohongshu(tenantID, account.ID, "miniapp-new", "new-secret"); err != nil {
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
