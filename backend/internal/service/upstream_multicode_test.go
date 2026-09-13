package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
)

func seedUpstreamMultiOrder(t *testing.T, endpoint string) model.Order {
	t.Helper()
	base := seedUpstreamWorkerOrder(t, endpoint)
	date := startOfDay(time.Now())
	order := model.Order{TenantID: base.TenantID, Channel: "online", ContactName: "测试", ContactPhone: "13800000000", Items: []model.OrderItem{{ProductID: base.Items[0].ProductID, Quantity: 2, UseDate: &date}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if len(order.Items[0].Tickets) != 2 {
		t.Fatalf("expected two sale-time tickets, got %d", len(order.Items[0].Tickets))
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", IdempotencyKey: "multicode-payment"}
	if err := (&PaymentService{}).CreatePayment(order.TenantID, &payment); err != nil {
		t.Fatal(err)
	}
	return order
}

func claimMultiSnapshot(t *testing.T, order model.Order) model.OrderItemSupplySnapshot {
	t.Helper()
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Microsecond)
	if err := model.DB.Model(&snapshot).Updates(map[string]interface{}{"locked_at": now, "issue_attempted_at": now, "provider_order_code": "PROVIDER", "provider_sub_order_code": "SUB"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&snapshot, snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestUpstreamMultiCodeBindsAtomicallyAndVerifiesIndependently(t *testing.T) {
	order := seedUpstreamMultiOrder(t, "https://supplier.example/api")
	snapshot := claimMultiSnapshot(t, order)
	worker := &UpstreamSupplyWorker{}
	for _, codes := range [][]string{{"ONLY-ONE"}, {"SAME", "SAME"}, {"OK", ""}} {
		if err := worker.finishIssueCodes(&snapshot, codes); err == nil {
			t.Fatalf("invalid set accepted: %v", codes)
		}
	}
	var other model.Ticket
	if err := model.DB.Where("order_id <> ?", order.ID).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := worker.finishIssueCodes(&snapshot, []string{"MUST-ROLL-BACK", other.TicketCode}); err == nil {
		t.Fatal("code belonging to another order was rebound")
	}
	var tickets []model.Ticket
	model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets)
	for i, ticket := range tickets {
		if ticket.Status != "pending_provider" || ticket.TicketCode != order.Items[0].Tickets[i].TicketCode {
			t.Fatal("incomplete code set partially issued")
		}
	}
	// Changing the product after sale cannot change this order's code count.
	if err := model.DB.Model(&model.Product{}).Where("id = ?", order.Items[0].ProductID).Update("code_mode", "order").Error; err != nil {
		t.Fatal(err)
	}
	if err := worker.finishIssueCodes(&snapshot, []string{"UPSTREAM-FIRST", "UPSTREAM-SECOND"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.finishIssueCodes(&snapshot, []string{"REPLACED-1", "REPLACED-2"}); err == nil {
		t.Fatal("ready binding was overwritten")
	}
	model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets)
	for i, ticket := range tickets {
		var entitlement model.TicketEntitlement
		if err := model.DB.Where("ticket_id = ?", ticket.ID).First(&entitlement).Error; err != nil {
			t.Fatal(err)
		}
		if ticket.Status != "unused" || ticket.TicketCode != []string{"UPSTREAM-FIRST", "UPSTREAM-SECOND"}[i] || entitlement.TicketCode != ticket.TicketCode {
			t.Fatal("ticket and entitlement binding mismatch")
		}
	}
	var devices []model.Device
	model.DB.Where("tenant_id = ?", order.TenantID).Order("id").Find(&devices)
	if err := (&TicketService{}).Verify(tickets[0].TicketCode, *devices[0].CheckPointID, devices[0].ID, order.TenantID+10000); err == nil {
		t.Fatal("foreign tenant accepted the supplier code")
	}
	for _, ticket := range tickets {
		if err := (&TicketService{}).Verify(ticket.TicketCode, *devices[0].CheckPointID, devices[0].ID, order.TenantID); err != nil {
			t.Fatal(err)
		}
		if err := (&TicketService{}).Verify(ticket.TicketCode, *devices[0].CheckPointID, devices[0].ID, order.TenantID); !errors.Is(err, ErrPointLimitReached) {
			t.Fatalf("per-ticket point limit: %v", err)
		}
	}
}

func TestUpstreamMultiCodeRejectsEntireBindingIfOneTicketIsRefunded(t *testing.T) {
	order := seedUpstreamMultiOrder(t, "https://supplier.example/api")
	snapshot := claimMultiSnapshot(t, order)
	if err := model.DB.Model(&order.Items[0].Tickets[1]).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&UpstreamSupplyWorker{}).finishIssueCodes(&snapshot, []string{"FIRST", "SECOND"}); err == nil {
		t.Fatal("issued a code set containing a refunded ticket")
	}
	var first model.Ticket
	model.DB.First(&first, order.Items[0].Tickets[0].ID)
	if first.Status != "pending_provider" {
		t.Fatal("other ticket exposed despite incomplete set")
	}
}

type upstreamMultiRefundFake struct{ upstreamRefundFake }

func (f *upstreamMultiRefundFake) QueryOrder(ctx context.Context, no string) (*zyb.QueryOrderResult, []byte, error) {
	r, raw, err := f.upstreamRefundFake.QueryOrder(ctx, no)
	if r != nil {
		r.Tickets[0].Quantity = "2"
	}
	return r, raw, err
}

func TestUpstreamMultiCodeWholeRefundLocksAndRefundsEveryTicket(t *testing.T) {
	order := seedUpstreamMultiOrder(t, "https://supplier.example/api")
	snapshot := claimMultiSnapshot(t, order)
	codes := []string{"REFUND-FIRST", "REFUND-SECOND"}
	if err := (&UpstreamSupplyWorker{}).finishIssueCodes(&snapshot, codes); err != nil {
		t.Fatal(err)
	}
	fake := &upstreamMultiRefundFake{upstreamRefundFake: upstreamRefundFake{pending: true}}
	svc := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return fake, nil }}
	if _, err := svc.CreateCashRefund(order.TenantID, order.OrderNo, "partial-upstream", 99.50, codes[:1], "部分退票"); err == nil {
		t.Fatal("partial local refund could cancel the entire supplier order")
	}
	refund, err := svc.CreateCashRefund(order.TenantID, order.OrderNo, "whole-upstream", 199, codes, "整单退票")
	if err != nil {
		t.Fatal(err)
	}
	var device model.Device
	model.DB.Where("tenant_id = ?", order.TenantID).First(&device)
	for _, code := range codes {
		if err := (&TicketService{}).Verify(code, *device.CheckPointID, device.ID, order.TenantID); err == nil {
			t.Fatal("refund did not lock every code")
		}
	}
	if _, err := svc.ProcessDigitalRefundTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if fake.cancels != 1 {
		t.Fatalf("expected one whole-order cancellation, got %d", fake.cancels)
	}
	fake.pending = false
	if _, err := svc.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(10*time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	if refund.Status != "succeeded" {
		t.Fatalf("refund not completed: %s", refund.Status)
	}
	for _, code := range codes {
		if err := (&TicketService{}).Verify(code, *device.CheckPointID, device.ID, order.TenantID); !errors.Is(err, ErrTicketRefunded) {
			t.Fatalf("refunded code not blocked: %v", err)
		}
	}
}

type upstreamMultiTestDecoder struct{ codes []string }

func (d upstreamMultiTestDecoder) Decode(context.Context, []byte) (string, error) {
	return "", errors.New("multi-code worker must not silently pick one code")
}
func (d upstreamMultiTestDecoder) DecodeAll(context.Context, []byte) ([]string, error) {
	return append([]string(nil), d.codes...), nil
}

func TestUpstreamMultiCodeWorkerRetriesOnlyImageAndBindsStableSet(t *testing.T) {
	var sends, images atomic.Int32
	date := time.Now().Format("2006-01-02")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		switch {
		case strings.Contains(xml, "<transactionName>SEND_CODE_REQ</transactionName>"):
			sends.Add(1)
			if !strings.Contains(xml, "<quantity>2</quantity>") {
				t.Error("one user order was split into supplier purchases")
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>0</code><orderResponse><order><orderCode>PROVIDER</orderCode><orderPrice>199.00</orderPrice><ticketOrders><ticketOrder><orderCode>SUB</orderCode><goodsCode>GOODS</goodsCode><quantity>2</quantity><price>99.50</price><totalPrice>199.00</totalPrice><occDate>%s</occDate></ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`, date)
		case strings.Contains(xml, "SEND_CODE_IMG_REQ"):
			if images.Add(1) == 1 {
				http.Error(w, "image pending", 503)
				return
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>0</code><img>%s</img></PWBResponse>`, base64.StdEncoding.EncodeToString([]byte("sheet")))
		default:
			http.Error(w, "unexpected supplier call", 500)
		}
	}))
	defer server.Close()
	order := seedUpstreamMultiOrder(t, server.URL)
	worker := UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}, nil
	}, Decoder: upstreamMultiTestDecoder{codes: []string{"CODE-Z", "CODE-A"}}}
	for _, now := range []time.Time{time.Now(), time.Now().Add(2 * time.Minute)} {
		if _, err := worker.ProcessTasks(context.Background(), now, 1); err != nil {
			t.Fatal(err)
		}
	}
	var tickets []model.Ticket
	model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets)
	if sends.Load() != 1 || images.Load() != 2 || tickets[0].TicketCode != "CODE-A" || tickets[1].TicketCode != "CODE-Z" || tickets[1].Status != "unused" {
		t.Fatalf("unexpected recovery sends=%d images=%d ticket statuses=%s,%s", sends.Load(), images.Load(), tickets[0].Status, tickets[1].Status)
	}
}

func TestXiaohongshuUpstreamMultiCodeCustomerRefundIncludesAllVouchers(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t, func(order *model.Order) {
		order.Items[0].Quantity = 2
		configureUpstreamFixture(t, order)
	})
	var link model.XiaohongshuOrderLink
	model.DB.Where("order_id = ?", f.order.ID).First(&link)
	before, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil || before.TicketIssuanceStatus != "pending" || len(before.TicketCodes) != 0 {
		t.Fatalf("pending supplier projection: %+v %v", before, err)
	}
	snapshot := claimMultiSnapshot(t, f.order)
	if err := (&UpstreamSupplyWorker{}).finishIssueCodes(&snapshot, []string{"XHS-UPSTREAM-A", "XHS-UPSTREAM-B"}); err != nil {
		t.Fatal(err)
	}
	ready, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil || ready.TicketIssuanceStatus != "ready" || len(ready.TicketCodes) != 2 {
		t.Fatalf("ready supplier projection: %+v %v", ready, err)
	}
	for _, ticket := range f.order.Items[0].Tickets {
		vouchers, err := xiaohongshuTicketVouchers(model.DB, &ticket)
		if err != nil || len(vouchers) != 1 {
			t.Fatalf("one external voucher per individual ticket: %v", err)
		}
	}
	fake := &upstreamMultiRefundFake{}
	factory := func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return fake, nil }
	miniapp := NewMiniappService()
	miniapp.NewUpstreamRefundClient = factory
	customer := xiaohongshuRefundCustomer(t, f)
	result, err := miniapp.ApplyXiaohongshuRefund(&customer, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "multi-code-customer", Reason: "整单退款"})
	if err != nil || result.Status != "processing" {
		t.Fatalf("refund application: %+v %v", result, err)
	}
	money, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	refundSvc := xiaohongshuRefundServiceForTest(t, server)
	refundSvc.NewUpstreamRefundClient = factory
	runXiaohongshuRefundWorker(t, refundSvc, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, refundSvc, time.Now().Add(time.Minute))
	var payment model.Payment
	model.DB.First(&payment, f.payment.ID)
	if payment.RefundedAmountCents != f.refundAmount || fake.cancels != 1 || len(money.addRequest.Vouchers) != 2 {
		t.Fatalf("refund amount=%d supplier cancels=%d vouchers=%d", payment.RefundedAmountCents, fake.cancels, len(money.addRequest.Vouchers))
	}
	after, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil || len(after.TicketCodes) != 0 {
		t.Fatalf("refunded tickets still visible: %+v %v", after, err)
	}
}
