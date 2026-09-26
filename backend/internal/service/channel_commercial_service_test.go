package service

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
)

func TestWechatMiniappChannelIsCommercialOnly(t *testing.T) {
	resetBusinessData(t)
	var tenant model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		tenant = model.Tenant{Name: "Commercial channel tenant", SystemCode: "COMMERCIAL-CHANNEL", Status: "active"}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: "restaurant", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	account := model.ChannelAccount{Code: "wechat-commercial", Status: "active", MemberMode: model.ChannelMemberModeFirstParty}
	channels := &ChannelService{}
	if err := channels.CreateWechatMiniapp(tenant.ID, &account, "wx-commercial-app", "wx-commercial-secret"); err != nil {
		t.Fatalf("create commercial channel: %v", err)
	}
	if account.Type != "wechat_miniapp" || account.SignAlgorithm != "access-token" || account.SecretCiphertext == "" || account.MemberMode != model.ChannelMemberModeDisabled {
		t.Fatalf("unexpected account: %+v", account)
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || secret != "wx-commercial-secret" || strings.Contains(account.SecretCiphertext, secret) {
		t.Fatalf("app secret was not encrypted: secret=%q err=%v", secret, err)
	}
	rows, err := channels.List(tenant.ID)
	if err != nil || len(rows) != 1 || rows[0].Type != "wechat_miniapp" || !rows[0].ProtocolConfigured || rows[0].SecretCiphertext != "" {
		t.Fatalf("commercial channel listing=%+v err=%v", rows, err)
	}

	for _, channelType := range []string{"ctrip", "xiaohongshu"} {
		candidate := model.ChannelAccount{Code: "commercial-" + channelType, Status: "active"}
		var createErr error
		if channelType == "ctrip" {
			createErr = channels.CreateCtrip(tenant.ID, &candidate, "commercial-ctrip", "sign-key", "1234567890abcdef", "abcdef1234567890")
		} else {
			createErr = channels.CreateXiaohongshu(tenant.ID, &candidate, "commercial-xhs", "secret")
		}
		if createErr == nil {
			t.Fatalf("commercial tenant created ticket channel %s", channelType)
		}
	}
}

func TestWechatMiniappAppIDIsUniqueAcrossTenants(t *testing.T) {
	resetBusinessData(t)
	createTenant := func(code string) uint {
		t.Helper()
		var tenant model.Tenant
		if err := model.Write(func(tx *gorm.DB) error {
			tenant = model.Tenant{Name: code, SystemCode: code, Status: "active"}
			if err := tx.Create(&tenant).Error; err != nil {
				return err
			}
			return tx.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: "retail", Status: "active"}).Error
		}); err != nil {
			t.Fatal(err)
		}
		return tenant.ID
	}
	firstTenantID := createTenant("COMMERCIAL-ONE")
	secondTenantID := createTenant("COMMERCIAL-TWO")
	channels := &ChannelService{}
	first := model.ChannelAccount{Code: "wechat-one", Status: "active"}
	if err := channels.CreateWechatMiniapp(firstTenantID, &first, "wx-duplicate-app", "secret-one"); err != nil {
		t.Fatal(err)
	}
	second := model.ChannelAccount{Code: "wechat-two", Status: "active"}
	if err := channels.CreateWechatMiniapp(secondTenantID, &second, "wx-duplicate-app", "secret-two"); err == nil {
		t.Fatal("duplicate WeChat AppID was accepted")
	}
}

func TestWechatMiniappCannotEnterTicketChannelOperations(t *testing.T) {
	resetBusinessData(t)
	var tenant model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		tenant = model.Tenant{Name: "Commercial ticket boundary", SystemCode: "COMMERCIAL-TICKET-BOUNDARY", Status: "active"}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: "restaurant", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{Code: "wechat-ticket-boundary", Status: "active"}
	channels := &ChannelService{}
	if err := channels.CreateWechatMiniapp(tenant.ID, &account, "wx-ticket-boundary", "secret"); err != nil {
		t.Fatalf("create commercial channel: %v", err)
	}
	want := ErrCommercialChannelTicketBoundary
	if _, err := channels.ListMappings(tenant.ID, account.ID); !errors.Is(err, want) {
		t.Fatalf("list mappings error=%v, want %v", err, want)
	}
	if _, _, err := channels.ListRequests(tenant.ID, account.ID, "", 1, 20); !errors.Is(err, want) {
		t.Fatalf("list requests error=%v, want %v", err, want)
	}
	if _, _, err := channels.ListOrders(tenant.ID, account.ID, "", "", 1, 20); !errors.Is(err, want) {
		t.Fatalf("list orders error=%v, want %v", err, want)
	}
	if _, err := channels.GetOrder(tenant.ID, account.ID, "ORD-NOT-ALLOWED"); !errors.Is(err, want) {
		t.Fatalf("get order error=%v, want %v", err, want)
	}
	if err := channels.AuthorizeRequestRetry(tenant.ID, account.ID, 1, 7, "admin", "boundary test"); !errors.Is(err, want) {
		t.Fatalf("authorize retry error=%v, want %v", err, want)
	}
	if _, err := channels.ImportBill(tenant.ID, account.ID, "commercial-bill-boundary", []ChannelBillInput{{ExternalNo: "x", Operation: "sale", AmountCents: 1}}); !errors.Is(err, want) {
		t.Fatalf("import bill error=%v, want %v", err, want)
	}
	if _, _, err := channels.ListReconciliations(tenant.ID, account.ID, 1, 20); !errors.Is(err, want) {
		t.Fatalf("list reconciliations error=%v, want %v", err, want)
	}
	if _, err := channels.GetReconciliation(tenant.ID, account.ID, 1); !errors.Is(err, want) {
		t.Fatalf("get reconciliation error=%v, want %v", err, want)
	}
}
