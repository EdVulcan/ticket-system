package service

import (
	"context"
	"testing"

	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
)

func TestCommerceStorefrontMemberModeFailsClosedAndCanBePlatformApproved(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	memberService := newMemberServiceForTest(t)
	fixture.service.Member = memberService
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).
		Update("member_mode", model.ChannelMemberModeDisabled).Error; err != nil {
		t.Fatalf("disable member source: %v", err)
	}
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "mode-disabled-subject")
	var session model.CommerceCustomerSession
	if err := model.DB.Where("token_hash = ?", storefrontHash(login.Token)).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.MemberID != nil {
		t.Fatalf("disabled channel created member association: %v", *session.MemberID)
	}
	var members int64
	if err := model.DB.Model(&model.TenantMember{}).Where("tenant_id = ?", fixture.tenantID).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("disabled channel created %d member records", members)
	}
	if err := (&ChannelService{}).SetMemberMode(fixture.tenantID, fixture.account.ID, model.ChannelMemberModeFirstParty); err != nil {
		t.Fatalf("approve member source: %v", err)
	}
	approvedLogin := storefrontLogin(t, fixture.service, fixture.account.AppID, "mode-approved-subject")
	session = model.CommerceCustomerSession{}
	if err := model.DB.Where("token_hash = ?", storefrontHash(approvedLogin.Token)).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.MemberID == nil || *session.MemberID == 0 {
		t.Fatal("approved first-party channel did not create member association")
	}
	if err := (&ChannelService{}).SetMemberMode(fixture.tenantID, fixture.account.ID, model.ChannelMemberModeDisabled); err != nil {
		t.Fatalf("disable member source after approval: %v", err)
	}
	authenticated, err := fixture.service.Authenticate(approvedLogin.Token)
	if err != nil {
		t.Fatalf("disabled channel invalidated storefront session: %v", err)
	}
	if authenticated.MemberID != nil {
		t.Fatalf("disabled channel exposed stale session member: %v", *authenticated.MemberID)
	}
}

func TestXiaohongshuMemberModeFailsClosedAndCanBePlatformApproved(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xhs-member-mode", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "xhs-member-mode-app", "secret"); err != nil {
		t.Fatal(err)
	}
	memberService := newMemberServiceForTest(t)
	server := miniappLoginServer(t)
	defer server.Close()
	miniapp := NewMiniappService()
	miniapp.Member = memberService
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	login, err := miniapp.LoginXiaohongshu(context.Background(), account.AppID, "login-code")
	if err != nil {
		t.Fatal(err)
	}
	var customer model.MiniappCustomer
	if err := model.DB.Where("session_token_hash = ?", hashMiniappValue(login.Token)).First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	if customer.MemberID != nil {
		t.Fatalf("disabled Xiaohongshu channel created member association: %v", *customer.MemberID)
	}
	if err := (&ChannelService{}).SetMemberMode(tenantID, account.ID, model.ChannelMemberModeFirstParty); err != nil {
		t.Fatalf("approve Xiaohongshu member source: %v", err)
	}
	approved, err := miniapp.LoginXiaohongshu(context.Background(), account.AppID, "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("session_token_hash = ?", hashMiniappValue(approved.Token)).First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	if customer.MemberID == nil || *customer.MemberID == 0 {
		t.Fatal("approved Xiaohongshu channel did not create member association")
	}
}
