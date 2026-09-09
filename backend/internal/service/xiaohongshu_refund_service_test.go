package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

type xiaohongshuRefundFixture struct {
	tenantID     uint
	account      model.ChannelAccount
	order        model.Order
	payment      model.Payment
	ticket       model.Ticket
	externalNo   string
	openID       string
	voucherCode  string
	refundAmount int64
}

// seedXiaohongshuRefundFixture creates the smallest production group-voucher
// order that satisfies the refund adapter's sale-time evidence checks.
func seedXiaohongshuRefundFixture(t *testing.T) xiaohongshuRefundFixture {
	t.Helper()
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	const appID = "xhs-refund-app"
	const token = "XiaohongshuRefundToken"
	const aesKey = "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	account := model.ChannelAccount{Code: "xhs-refund"}
	if err := (&ChannelService{}).CreateXiaohongshuIntegration(tenantID, &account, appID, "refund-secret", token, aesKey); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-REFUND-PRODUCT", ChannelSaleCents: 9950, Status: "active"}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "XHS-REFUND-SKU", CategoryID: "ticket", ImageURL: "https://example.com/refund.png", Description: "退款测试门票",
		ProductPath: "/pages/product", OrderPath: "/pages/order", ProductType: xiaohongshu.ProductTypeGroupVoucher, SettleType: 1,
		SyncStatus: "synced", AuditStatus: "approved",
	}).Error; err != nil {
		t.Fatal(err)
	}
	openID := "XHS-REFUND-OPEN"
	openCiphertext, err := utils.EncryptAES(openID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCiphertext, err := utils.EncryptAES("XHS-REFUND-SESSION")
	if err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue(openID), OpenIDCiphertext: openCiphertext, SessionKeyCiphertext: sessionCiphertext, SessionTokenHash: hashMiniappValue("XHS-REFUND-TOKEN"), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	externalNo := "XHS-REFUND-ORDER"
	order := model.Order{TenantID: tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Preload("Items.Product").Preload("Items.Tickets").First(&order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(order.Items) != 1 || len(order.Items[0].Tickets) != 1 {
		t.Fatalf("unexpected local order shape: %+v", order)
	}
	ticket := order.Items[0].Tickets[0]
	amountCents := moneyCents(order.TotalAmount)
	now := time.Now()
	payment := model.Payment{TenantID: tenantID, PaymentNo: fmt.Sprintf("PAY-XHS-REFUND-%d", now.UnixNano()), IdempotencyKey: fmt.Sprintf("xhs-refund-payment-%d", now.UnixNano()), OrderNo: order.OrderNo, Amount: order.TotalAmount, AmountCents: amountCents, Method: "xiaohongshu", PayType: "life_gpay", Status: "paid", TransactionID: "XHS-TRADE-REFUND", PaidAt: &now}
	if err := model.DB.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{TenantID: tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ClientRequestID: "xhs-refund-request", ExternalOrderID: externalNo, PlatformOrderID: "XHS-PLATFORM-REFUND", State: "paid", VoucherIssuanceStatus: "pending"}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	voucherCode := "XHS-REFUND-VOUCHER"
	voucherCiphertext, err := utils.EncryptAES(voucherCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuVoucherLink{TenantID: tenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID, TicketID: ticket.ID, VoucherCodeHash: hashMiniappValue(voucherCode), VoucherCodeCiphertext: voucherCiphertext, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&link).Update("voucher_issuance_status", "ready").Error; err != nil {
		t.Fatal(err)
	}
	snapshotRequest := xiaohongshu.OrderUpsertRequest{
		ExternalOrderID: externalNo, OpenID: openID, Path: "/pages/order", CreatedAt: now.Unix(),
		Products: []xiaohongshu.OrderProduct{{ExternalProductID: mapping.ExternalCode, ExternalSKUID: "XHS-REFUND-SKU", Count: 1, SalePrice: amountCents, RealPrice: amountCents}},
		Price:    xiaohongshu.OrderPrice{OrderPrice: amountCents},
	}
	snapshot, err := encryptXiaohongshuOrderOperationPayload(snapshotRequest, xiaohongshu.ProductTypeGroupVoucher)
	if err != nil {
		t.Fatal(err)
	}
	payTokenCiphertext, err := utils.EncryptAES("XHS-REFUND-PAY-TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	payTokenExpiresAt := now.Add(time.Hour)
	if err := model.DB.Create(&model.XiaohongshuOrderOperation{TenantID: tenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID, RequestPayloadCiphertext: snapshot, PlatformOrderID: link.PlatformOrderID, PayTokenCiphertext: payTokenCiphertext, PayTokenExpiresAt: &payTokenExpiresAt, Status: "completed", CompletedAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	return xiaohongshuRefundFixture{tenantID: tenantID, account: account, order: order, payment: payment, ticket: ticket, externalNo: externalNo, openID: openID, voucherCode: voucherCode, refundAmount: amountCents}
}

type xiaohongshuRefundFake struct {
	t          *testing.T
	status     int
	addFailure bool
	mismatch   bool
	addCalls   atomic.Int32
	getCalls   atomic.Int32
	addRequest xiaohongshu.AfterSalesAddRequest
}

func newXiaohongshuRefundFake(t *testing.T, status int, addFailure bool) (*xiaohongshuRefundFake, *httptest.Server) {
	t.Helper()
	fake := &xiaohongshuRefundFake{t: t, status: status, addFailure: addFailure}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"XHS-REFUND-TOKEN","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/after_sales_order/add":
			fake.addCalls.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&fake.addRequest); err != nil {
				t.Errorf("decode add request: %v", err)
			}
			if fake.addFailure {
				http.Error(w, "lost add reply", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/after_sales_order/get":
			fake.getCalls.Add(1)
			var request xiaohongshu.AfterSalesGetRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode get request: %v", err)
			}
			if request.ExternalOrderID != fake.addRequest.ExternalOrderID || request.ExternalAfterSalesOrderID != fake.addRequest.ExternalAfterSalesOrderID || request.OpenID != fake.addRequest.OpenID {
				t.Errorf("get request did not retain add identity: %+v add=%+v", request, fake.addRequest)
			}
			price := fake.addRequest.Price.RefundPrice
			if fake.mismatch {
				price--
			}
			response := xiaohongshu.AfterSalesOrderResponse{ExternalOrderID: request.ExternalOrderID, ExternalAfterSalesOrderID: request.ExternalAfterSalesOrderID, OpenID: request.OpenID, Status: fake.status, Type: 1, ProductType: xiaohongshu.ProductTypeGroupVoucher, Price: xiaohongshu.AfterSalesPriceInfo{RefundPrice: price}, Vouchers: fake.addRequest.Vouchers}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": response, "success": true, "msg": "success", "code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	return fake, server
}

func xiaohongshuRefundServiceForTest(t *testing.T, server *httptest.Server) *RefundService {
	t.Helper()
	return &RefundService{NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
		if appID != "xhs-refund-app" || secret != "refund-secret" || environment != "production" {
			t.Fatalf("unexpected Xiaohongshu credentials app=%q secret=%q environment=%q", appID, secret, environment)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
}

func createXiaohongshuRefund(t *testing.T, fixture xiaohongshuRefundFixture, idempotencyKey string) *model.Refund {
	t.Helper()
	refund, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, idempotencyKey, float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Method != "xiaohongshu" || refund.ParentRefundID != 0 || refund.Status != "pending" {
		t.Fatalf("Xiaohongshu auto route did not create a single pending provider refund: %+v", refund)
	}
	return refund
}

func runXiaohongshuRefundWorker(t *testing.T, service *RefundService, now time.Time) {
	t.Helper()
	if _, err := service.ProcessDigitalRefundTasks(context.Background(), now, 1); err != nil {
		t.Fatal(err)
	}
}

func TestXiaohongshuRefundCompletesExactlyOnceAfterAddAndQuery(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-success")
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)

	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second)) // add accepted
	var pendingTicket model.Ticket
	if err := model.DB.First(&pendingTicket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pendingTicket.PendingRefundID != refund.ID || pendingTicket.Status != "unused" || fake.addCalls.Load() != 1 || fake.getCalls.Load() != 0 {
		t.Fatalf("accepted add changed local facts or calls add=%d get=%d ticket=%+v", fake.addCalls.Load(), fake.getCalls.Load(), pendingTicket)
	}

	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Minute)) // get status=2
	runXiaohongshuRefundWorker(t, service, time.Now().Add(2*time.Minute))
	var storedRefund model.Refund
	var storedPayment model.Payment
	var storedOrder model.Order
	if err := model.DB.First(&storedRefund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&pendingTicket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&storedPayment, fixture.payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&storedOrder, fixture.order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 || storedRefund.Status != "succeeded" || pendingTicket.Status != "refunded" || pendingTicket.PendingRefundID != 0 || storedPayment.RefundedAmountCents != fixture.refundAmount || storedOrder.Status != "refunded" {
		t.Fatalf("refund did not converge once: add=%d get=%d refund=%+v payment=%+v ticket=%+v order=%+v", fake.addCalls.Load(), fake.getCalls.Load(), storedRefund, storedPayment, pendingTicket, storedOrder)
	}
}

func TestXiaohongshuRefundPendingKeepsReservationAndFailureReleasesIt(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        int
		refundStatus  string
		pendingTicket bool
	}{
		{name: "pending", status: 1, refundStatus: "pending", pendingTicket: true},
		{name: "failed", status: 3, refundStatus: "failed", pendingTicket: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedXiaohongshuRefundFixture(t)
			refund := createXiaohongshuRefund(t, fixture, "xhs-refund-"+test.name)
			fake, server := newXiaohongshuRefundFake(t, test.status, false)
			defer server.Close()
			service := xiaohongshuRefundServiceForTest(t, server)
			runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second))
			runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Minute))
			var storedRefund model.Refund
			var ticket model.Ticket
			if err := model.DB.First(&storedRefund, refund.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil {
				t.Fatal(err)
			}
			if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 || storedRefund.Status != test.refundStatus || (ticket.PendingRefundID == refund.ID) != test.pendingTicket || ticket.Status != "unused" {
				t.Fatalf("status=%d did not preserve expected local state refund=%+v ticket=%+v add=%d get=%d", test.status, storedRefund, ticket, fake.addCalls.Load(), fake.getCalls.Load())
			}
		})
	}
}

func TestXiaohongshuRefundLostAddReplyOnlyQueriesOnRecovery(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-lost-add")
	fake, server := newXiaohongshuRefundFake(t, 2, true)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)

	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second))
	fake.addFailure = false
	runXiaohongshuRefundWorker(t, service, time.Now().Add(2*time.Minute))
	var storedRefund model.Refund
	if err := model.DB.First(&storedRefund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 || storedRefund.Status != "succeeded" {
		t.Fatalf("lost add recovery resent request or failed to finalize: add=%d get=%d refund=%+v", fake.addCalls.Load(), fake.getCalls.Load(), storedRefund)
	}
}

func TestXiaohongshuRefundQueryMismatchKeepsLocalFactsReserved(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-query-mismatch")
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	fake.mismatch = true
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Minute))
	var storedRefund model.Refund
	var ticket model.Ticket
	var payment model.Payment
	if err := model.DB.First(&storedRefund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&payment, fixture.payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 || storedRefund.Status != "pending" || ticket.Status != "unused" || ticket.PendingRefundID != refund.ID || payment.RefundedAmountCents != 0 {
		t.Fatalf("mismatched provider result changed local facts: add=%d get=%d refund=%+v ticket=%+v payment=%+v", fake.addCalls.Load(), fake.getCalls.Load(), storedRefund, ticket, payment)
	}
	var task model.DigitalRefundTask
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "manual_review" {
		t.Fatalf("mismatched result must require review, got %s", task.Status)
	}
}

func TestXiaohongshuRefundIdempotencyAndOperationIdentityGuards(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-same-key")
	replayed, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, "xhs-refund-same-key", float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消")
	if err != nil || replayed.ID != refund.ID {
		t.Fatalf("same-key retry=%+v err=%v, want refund %d", replayed, err, refund.ID)
	}
	var operations int64
	if err := model.DB.Model(&model.XiaohongshuRefundOperation{}).Where("refund_id = ?", refund.ID).Count(&operations).Error; err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Fatalf("same-key request created %d refund operations", operations)
	}
	var operation model.XiaohongshuRefundOperation
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&operation).Update("external_after_sales_order_id", "forged-after-sale").Error; err == nil {
		t.Fatal("refund operation external after-sale identity was mutable")
	}
	other := model.Tenant{Name: "Other Refund Tenant", SystemCode: fmt.Sprintf("OTHER-REFUND-%d", time.Now().UnixNano()), SecretKey: "other-secret"}
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&operation).Update("tenant_id", other.ID).Error; err == nil {
		t.Fatal("refund operation tenant ownership was mutable")
	}
}

func TestXiaohongshuRefundFinalProviderFailureCannotBeRetried(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-final-failure")
	fake, server := newXiaohongshuRefundFake(t, 3, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Minute))
	var task model.DigitalRefundTask
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" || fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 {
		t.Fatalf("final provider failure did not become terminal: task=%+v add=%d get=%d", task, fake.addCalls.Load(), fake.getCalls.Load())
	}
	if err := service.RetryDigitalRefundTask(fixture.tenantID, task.ID, 1, "admin", "provider already rejected this after-sale request"); err == nil {
		t.Fatal("terminal Xiaohongshu provider failure became retryable")
	}
}

func TestXiaohongshuRefundRejectsCrossTenantUsedPartialAndGroupMismatches(t *testing.T) {
	t.Run("cross tenant", func(t *testing.T) {
		fixture := seedXiaohongshuRefundFixture(t)
		otherTenant, _ := seedSellableProduct(t, "unlimited", 0)
		if _, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: otherTenant}, fixture.order.OrderNo, "xhs-refund-cross-tenant", float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消"); err == nil {
			t.Fatal("cross-tenant refund was accepted")
		}
	})
	t.Run("used ticket", func(t *testing.T) {
		fixture := seedXiaohongshuRefundFixture(t)
		if err := model.DB.Model(&model.Ticket{}).Where("id = ?", fixture.ticket.ID).Updates(map[string]interface{}{"status": "used", "check_in_count": 1}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, "xhs-refund-used", float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消"); err == nil {
			t.Fatal("used Xiaohongshu ticket refund was accepted")
		}
	})
	t.Run("partial amount", func(t *testing.T) {
		fixture := seedXiaohongshuRefundFixture(t)
		if _, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, "xhs-refund-partial", 1, []string{fixture.ticket.TicketCode}, "游客取消"); err == nil {
			t.Fatal("partial Xiaohongshu refund was accepted")
		}
	})
	t.Run("non group product", func(t *testing.T) {
		fixture := seedXiaohongshuRefundFixture(t)
		var original model.XiaohongshuOrderOperation
		if err := model.DB.Where("tenant_id = ?", fixture.tenantID).First(&original).Error; err != nil {
			t.Fatal(err)
		}
		payload, err := decryptXiaohongshuOrderOperationPayload(original.RequestPayloadCiphertext)
		if err != nil {
			t.Fatal(err)
		}
		nonGroupSnapshot, err := encryptXiaohongshuOrderOperationPayload(payload.Request, xiaohongshu.ProductTypePresaleVoucher)
		if err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&original).Update("request_payload_ciphertext", nonGroupSnapshot).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, "xhs-refund-not-group", float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消"); err == nil {
			t.Fatal("non-group Xiaohongshu refund was accepted")
		}
	})
}

func TestXiaohongshuRefundKnownWebhookResumesQueryWithoutAccountHold(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "xhs-refund-webhook")
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second)) // durable add accepted
	if err := model.DB.Model(&model.DigitalRefundTask{}).Where("refund_id = ?", refund.ID).Updates(map[string]interface{}{"status": "manual_review", "next_attempt_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	const token = "XiaohongshuRefundToken"
	const aesKey = "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	payload := []byte(fmt.Sprintf(`{"Event":"REFUND_RESULT","OutAfterSalesOrderId":%q,"Status":2}`, refund.RefundNo))
	encrypted := encryptXiaohongshuWebhookFixture(t, aesKey, payload, fixture.account.AppID)
	message := XiaohongshuWebhookMessage{Nonce: "xhs-refund-wakeup", Timestamp: 1700000005, Encrypt: encrypted, MsgSignature: xiaohongshu.MessageSignature(token, strconv.FormatInt(1700000005, 10), "xhs-refund-wakeup", encrypted)}
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), fixture.account.AppID, message); err != nil {
		t.Fatal(err)
	}
	var task model.DigitalRefundTask
	var holds int64
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuRefundCoordination{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&holds).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "submitted" || holds != 0 {
		t.Fatalf("known callback did not resume exact task without account hold: task=%+v holds=%d", task, holds)
	}
	if task.NextAttemptAt == nil || task.NextAttemptAt.After(time.Now()) {
		t.Fatal("authenticated callback did not make refund query immediately due")
	}
	runXiaohongshuRefundWorker(t, service, time.Now())
	if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 {
		t.Fatalf("webhook resume re-sent add or skipped query: add=%d get=%d", fake.addCalls.Load(), fake.getCalls.Load())
	}
}
