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
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, "miniapp-1", "app-secret-1", "MessageToken123", "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"); err != nil {
		t.Fatal(err)
	}
	if account.Type != "xiaohongshu" || account.Status != "active" || account.Environment != "production" {
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
