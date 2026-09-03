package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

type xiaohongshuVoucherFixture struct {
	tenantID   uint
	checkpoint model.CheckPoint
	device     model.Device
	ticket     model.Ticket
	link       model.XiaohongshuVoucherLink
}

func seedXiaohongshuVoucherFixture(t *testing.T) xiaohongshuVoucherFixture {
	t.Helper()
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: fmt.Sprintf("xhs-verify-%d", time.Now().UnixNano()), Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-verify", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: fmt.Sprintf("XHS-PRODUCT-%d", time.Now().UnixNano()), DisplayName: "测试门票", ChannelSaleCents: 1, Status: "active"}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var fixtureCheckpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&fixtureCheckpoint).Error; err != nil {
		t.Fatal(err)
	}
	var fixtureDevice model.Device
	if err := model.DB.Where("tenant_id = ? AND check_point_id = ?", tenantID, fixtureCheckpoint.ID).First(&fixtureDevice).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	// The sandbox channel intentionally creates sandbox tickets that cannot
	// open a real gate. Promote this fixture to a production ticket so the
	// coordination behavior itself can be exercised.
	if err := model.DB.Model(&ticket).Update("environment", "production").Error; err != nil {
		t.Fatal(err)
	}
	ticket.Environment = "production"
	openIDCiphertext, err := utils.EncryptAES("OPEN-ID-FIXTURE")
	if err != nil {
		t.Fatal(err)
	}
	sessionKeyCiphertext, err := utils.EncryptAES("SESSION-KEY-FIXTURE")
	if err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("OPEN-ID-FIXTURE"), OpenIDCiphertext: openIDCiphertext, SessionKeyCiphertext: sessionKeyCiphertext, SessionTokenHash: hashMiniappValue("TOKEN-FIXTURE"), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	openID := customer.ID
	externalOrderID := fmt.Sprintf("XHS-ORDER-%d", time.Now().UnixNano())
	orderLink := model.XiaohongshuOrderLink{TenantID: tenantID, ChannelAccountID: account.ID, MiniappCustomerID: openID, OrderID: order.ID, ClientRequestID: "fixture", ExternalOrderID: externalOrderID, State: "paid"}
	if err := model.DB.Create(&orderLink).Error; err != nil {
		t.Fatal(err)
	}
	voucherCode := fmt.Sprintf("XHS-CODE-%d", time.Now().UnixNano())
	ciphertext, err := utils.EncryptAES(voucherCode)
	if err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuVoucherLink{TenantID: tenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: orderLink.ID, TicketID: ticket.ID, VoucherCodeHash: hashMiniappValue(voucherCode), VoucherCodeCiphertext: ciphertext, Status: 1}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	return xiaohongshuVoucherFixture{tenantID: tenantID, checkpoint: fixtureCheckpoint, device: fixtureDevice, ticket: ticket, link: link}
}

func TestXiaohongshuVoucherVerificationCommitsExternalAndLocalFactsOnce(t *testing.T) {
	fixture := seedXiaohongshuVoucherFixture(t)
	var remoteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/voucher/verify":
			remoteCalls.Add(1)
			var request xiaohongshu.VoucherVerifyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if request.ExternalOrderID == "" || len(request.Vouchers) != 1 || request.Vouchers[0].Code == "" {
				t.Errorf("unexpected request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"verify_id":"VERIFY-ONCE"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc := NewDeviceService(model.DB, &TicketService{})
	svc.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		if appID != "miniapp-verify" || secret != "app-secret" || environment != "sandbox" {
			t.Fatalf("client credentials app=%q secret=%q environment=%q", appID, secret, environment)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	req := DirectVerifyRequest{TenantID: fixture.tenantID, DeviceID: fixture.device.ID, CheckPointID: fixture.checkpoint.ID, RequestID: "xhs-scan-once", RequestHash: "xhs-body-once", TicketCode: mustDecryptVoucher(t, fixture.link)}
	first, err := svc.VerifyDirect(req)
	if err != nil || first.Result != "allow" {
		var saga model.XiaohongshuVoucherVerification
		var debugTicket model.Ticket
		_ = model.DB.Preload("OrderItem.Product").Where("id = ?", fixture.ticket.ID).First(&debugTicket).Error
		var debugOrder model.Order
		_ = model.DB.Where("id = ?", debugTicket.OrderID).First(&debugOrder).Error
		_ = model.DB.Where("voucher_link_id = ?", fixture.link.ID).First(&saga).Error
		t.Fatalf("first response=%+v err=%v saga=%+v ticket=%+v order=%+v", first, err, saga, debugTicket, debugOrder)
	}
	second, err := svc.VerifyDirect(req)
	if err != nil || second.Result != "allow" {
		t.Fatalf("replayed response=%+v err=%v", second, err)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote verify calls=%d, want 1", remoteCalls.Load())
	}
	var storedLink model.XiaohongshuVoucherLink
	if err := model.DB.First(&storedLink, fixture.link.ID).Error; err != nil || storedLink.VerifyID != "VERIFY-ONCE" {
		t.Fatalf("voucher link=%+v err=%v", storedLink, err)
	}
	var saga model.XiaohongshuVoucherVerification
	if err := model.DB.Where("voucher_link_id = ?", fixture.link.ID).First(&saga).Error; err != nil || saga.State != "local_completed" || saga.VerifyID != "VERIFY-ONCE" || saga.CheckInRecordID == 0 {
		t.Fatalf("saga=%+v err=%v", saga, err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil || ticket.Status != "used" || ticket.PendingXiaohongshuVerificationID != 0 {
		t.Fatalf("ticket=%+v err=%v", ticket, err)
	}
	var successful int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND result = ?", fixture.ticket.ID, "success").Count(&successful).Error; err != nil || successful != 1 {
		t.Fatalf("successful check-ins=%d err=%v", successful, err)
	}
}

func TestXiaohongshuVoucherVerificationPreparedRecoversAfterProcessRestart(t *testing.T) {
	fixture := seedXiaohongshuVoucherFixture(t)
	var remoteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/voucher/verify":
			remoteCalls.Add(1)
			_, _ = w.Write([]byte(`{"data":{"verify_id":"VERIFY-AFTER-RESTART"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := DirectVerifyRequest{
		TenantID: fixture.tenantID, DeviceID: fixture.device.ID, CheckPointID: fixture.checkpoint.ID,
		RequestID: "xhs-restart-scan", RequestHash: "xhs-restart-body", TicketCode: fixture.ticket.TicketCode,
	}
	first := NewDeviceService(model.DB, &TicketService{})
	first.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	verification, replay, err := first.beginDeviceVerification(request, fixture.device.ScenicAreaID)
	if err != nil || replay != nil {
		t.Fatalf("begin verification=%+v replay=%+v err=%v", verification, replay, err)
	}
	saga, err := first.ensureXiaohongshuVoucherVerification(request, &fixture.link, verification.ID)
	if err != nil {
		t.Fatalf("prepare saga: %v", err)
	}
	if saga.State != "prepared" {
		t.Fatalf("saga state=%q, want prepared", saga.State)
	}

	// A new service instance stands in for a process that died before it could
	// reserve the ticket or call Xiaohongshu.
	restarted := NewDeviceService(model.DB, &TicketService{})
	restarted.NewXiaohongshuClient = first.NewXiaohongshuClient
	processed, err := restarted.ProcessPendingXiaohongshuVoucherVerifications(context.Background(), time.Now(), 20)
	if err != nil || processed != 1 {
		t.Fatalf("recovery processed=%d err=%v", processed, err)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote verify calls=%d, want 1", remoteCalls.Load())
	}

	var storedSaga model.XiaohongshuVoucherVerification
	if err := model.DB.First(&storedSaga, saga.ID).Error; err != nil || storedSaga.State != "local_completed" || storedSaga.VerifyID != "VERIFY-AFTER-RESTART" || storedSaga.CheckInRecordID == 0 {
		t.Fatalf("recovered saga=%+v err=%v", storedSaga, err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil || ticket.Status != "used" || ticket.PendingXiaohongshuVerificationID != 0 {
		t.Fatalf("recovered ticket=%+v err=%v", ticket, err)
	}
	var storedVerification model.DeviceVerification
	if err := model.DB.First(&storedVerification, verification.ID).Error; err != nil || storedVerification.Status != "completed" || storedVerification.Result != "allow" {
		t.Fatalf("recovered device verification=%+v err=%v", storedVerification, err)
	}
}

func TestXiaohongshuVoucherVerificationUnknownFailsClosedAndBlocksBypass(t *testing.T) {
	fixture := seedXiaohongshuVoucherFixture(t)
	var remoteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/voucher/verify":
			remoteCalls.Add(1)
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc := NewDeviceService(model.DB, &TicketService{})
	svc.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	voucherCode := mustDecryptVoucher(t, fixture.link)
	first, err := svc.VerifyDirect(DirectVerifyRequest{TenantID: fixture.tenantID, DeviceID: fixture.device.ID, CheckPointID: fixture.checkpoint.ID, RequestID: "xhs-scan-unknown", RequestHash: "xhs-body-unknown", TicketCode: voucherCode})
	if err != nil || first.Result != "deny" || first.Code != 409 {
		var saga model.XiaohongshuVoucherVerification
		_ = model.DB.Where("voucher_link_id = ?", fixture.link.ID).First(&saga).Error
		t.Fatalf("unknown response=%+v err=%v saga=%+v", first, err, saga)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote verify calls=%d, want 1", remoteCalls.Load())
	}
	second, err := svc.VerifyDirect(DirectVerifyRequest{TenantID: fixture.tenantID, DeviceID: fixture.device.ID, CheckPointID: fixture.checkpoint.ID, RequestID: "xhs-scan-retry", RequestHash: "xhs-body-retry", TicketCode: voucherCode})
	if err != nil || second.Result != "deny" || second.Code != 409 {
		t.Fatalf("retry response=%+v err=%v", second, err)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("unknown result triggered duplicate remote call=%d", remoteCalls.Load())
	}
	if err := (&TicketService{}).Verify(fixture.ticket.TicketCode, fixture.checkpoint.ID, fixture.device.ID, fixture.tenantID); !errors.Is(err, ErrTicketUnavailable) {
		t.Fatalf("ordinary ticket path err=%v, want reservation denial", err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil || ticket.PendingXiaohongshuVerificationID == 0 || ticket.Status != "unused" {
		t.Fatalf("unknown ticket=%+v err=%v", ticket, err)
	}
}

func mustDecryptVoucher(t *testing.T, link model.XiaohongshuVoucherLink) string {
	t.Helper()
	value, err := utils.DecryptAES(link.VoucherCodeCiphertext)
	if err != nil || strings.TrimSpace(value) == "" {
		t.Fatalf("decrypt voucher err=%v", err)
	}
	return value
}
