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
	"ticket-backend/internal/zyb"
	"time"
)

func seedGroupedXhs(t *testing.T, upstream bool) xiaohongshuRefundFixture {
	t.Helper()
	f := seedXiaohongshuRefundFixture(t, func(order *model.Order) {
		order.Items[0].Quantity = 2
		if err := model.DB.Model(&model.Product{}).Where("id = ?", order.Items[0].ProductID).Update("code_mode", "order").Error; err != nil {
			t.Fatal(err)
		}
		if upstream {
			configureUpstreamFixture(t, order)
		}
		var p model.Product
		model.DB.First(&p, order.Items[0].ProductID)
		var group model.RuleGroup
		model.DB.Where("rule_id = ?", p.RuleID).First(&group)
		if err := model.DB.Model(&group).Update("max_total_check_in", 2).Error; err != nil {
			t.Fatal(err)
		}
		cp := model.CheckPoint{TenantID: order.TenantID, ScenicAreaID: p.ScenicAreaID, Name: "第二入口"}
		if err := model.DB.Create(&cp).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: cp.ID, MaxPerCheckIn: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Create(&model.Device{TenantID: order.TenantID, ScenicAreaID: p.ScenicAreaID, Name: "第二设备", SerialNumber: "GROUP-SECOND", Type: "gate", Status: "online", CheckPointID: &cp.ID, AuthKeyCiphertext: encryptedDeviceKeyForTest(t, "key")}).Error; err != nil {
			t.Fatal(err)
		}
	})
	return f
}

func TestXiaohongshuGroupVoucherVerifyOnceAndRefundAll(t *testing.T) {
	f := seedGroupedXhs(t, true)
	readyUpstreamFixture(t, f.order.ID, &f.ticket)
	var members []model.XiaohongshuVoucherLink
	model.DB.Where("ticket_id = ?", f.ticket.ID).Order("id").Find(&members)
	if len(members) != 2 {
		t.Fatalf("group member count=%d", len(members))
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/rmp/token" {
			fmt.Fprint(w, `{"success":true,"code":0,"data":{"access_token":"T","expire_in":7200}}`)
			return
		}
		if r.URL.Path == "/api/rmp/mp/deal/voucher/verify" {
			calls.Add(1)
			var req xiaohongshu.VoucherVerifyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if len(req.Vouchers) != 2 {
				t.Errorf("sent %d vouchers instead of the complete group", len(req.Vouchers))
			}
			fmt.Fprint(w, `{"success":true,"code":0,"data":{"verify_id":"VERIFY-GROUP"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	svc := NewDeviceService(model.DB, &TicketService{})
	svc.NewXiaohongshuClient = func(a, b, c string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: a, Secret: b, BaseURL: server.URL, HTTP: server.Client()}
	}
	var devices []model.Device
	model.DB.Where("tenant_id = ?", f.tenantID).Order("id").Find(&devices)
	for i, d := range devices {
		code := f.ticket.TicketCode
		if i > 0 {
			code = mustDecryptVoucher(t, members[1])
		} // Individual platform alias resolves to the SAME group.
		response, err := svc.VerifyDirect(DirectVerifyRequest{TenantID: f.tenantID, DeviceID: d.ID, CheckPointID: *d.CheckPointID, RequestID: fmt.Sprintf("group-%d", i), RequestHash: fmt.Sprintf("hash-%d", i), TicketCode: code})
		if err != nil || response.Result != "allow" {
			t.Fatalf("point %d: %+v %v", i, response, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("batch verification repeated %d times", calls.Load())
	}
	var sagas int64
	model.DB.Model(&model.XiaohongshuVoucherVerification{}).Where("ticket_id = ?", f.ticket.ID).Count(&sagas)
	if sagas != 1 {
		t.Fatalf("sagas=%d", sagas)
	}
	model.DB.Where("ticket_id = ?", f.ticket.ID).Find(&members)
	for _, member := range members {
		if member.VerifyID != "VERIFY-GROUP" {
			t.Fatal("group verify identity not synchronized")
		}
	}
	actor := recoveryAdmin(t, f.tenantID)
	refund, err := (&RefundService{}).CreateMixedRefundAs(actor, f.order.OrderNo, "group-used-refund", f.order.TotalAmount, []string{f.ticket.TicketCode}, "误核销退款")
	if err != nil {
		t.Fatal(err)
	}
	var op model.XiaohongshuRefundOperation
	model.DB.Where("refund_id = ?", refund.ID).First(&op)
	raw, err := utils.DecryptAES(op.RequestPayloadCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	var request xiaohongshu.AfterSalesAddRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, v := range request.Vouchers {
		total += v.RefundPrice
	}
	if len(request.Vouchers) != 2 || total != f.refundAmount || request.Products[0].Count != 2 {
		t.Fatalf("refund group count=%d total=%d", len(request.Vouchers), total)
	}
}

func TestXiaohongshuGroupCustomerRefundWithDocumentedZybResponse(t *testing.T) {
	f := seedGroupedXhs(t, true)
	var link model.XiaohongshuOrderLink
	model.DB.Where("order_id = ?", f.order.ID).First(&link)
	before, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil || before.TicketIssuanceStatus != "pending" || len(before.TicketCodes) != 0 {
		t.Fatalf("supplier issuance projection: %+v %v", before, err)
	}
	readyUpstreamFixture(t, f.order.ID, &f.ticket)
	var cancellations atomic.Int32
	var wrongIdentity atomic.Bool
	third := upstreamThirdPartyChild(f.order.OrderNo, f.order.Items[0].ID)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		switch {
		case strings.Contains(xml, "QUERY_ORDER_NEW_REQ"):
			child := third
			if wrongIdentity.Load() {
				child = "OTHER-ORDER"
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>QUERY_ORDER_NEW_RES</transactionName><code>0</code><order><orderCode>PROVIDER</orderCode><ticketOrders><ticketOrder><scenicThirdCode>%s</scenicThirdCode><goodsCode>GOODS</goodsCode><quantity>2</quantity><returnNum>0</returnNum><alreadyCheckNum>0</alreadyCheckNum><price>99.5</price><totalPrice>199</totalPrice></ticketOrder></ticketOrders></order></PWBResponse>`, child)
		case strings.Contains(xml, "CHECK_STATUS_QUERY_REQ"):
			fmt.Fprintf(w, `<PWBResponse><transactionName>CHECK_STATUS_QUERY_RES</transactionName><code>0</code><subOrders><subOrder><orderCode>%s_1</orderCode><needCheckNum>2</needCheckNum><alreadyCheckNum>0</alreadyCheckNum><returnNum>0</returnNum><checkStatus>un_check</checkStatus></subOrder></subOrders></PWBResponse>`, third)
		case strings.Contains(xml, "SEND_CODE_CANCEL_NEW_REQ"):
			cancellations.Add(1)
			fmt.Fprint(w, `<PWBResponse><transactionName>SEND_CODE_CANCEL_RES</transactionName><code>0</code><retreatBatchNo>B</retreatBatchNo></PWBResponse>`)
		case strings.Contains(xml, "QUERY_RETREAT_STATUS_REQ"):
			fmt.Fprint(w, `<PWBResponse><transactionName>QUERY_RETREAT_STATUS_RES</transactionName><code>0</code></PWBResponse>`)
		default:
			http.Error(w, "unexpected supplier call", 500)
		}
	}))
	defer provider.Close()
	factory := func(*model.UpstreamConnection) (UpstreamRefundClient, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: provider.URL, CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: provider.Client()}, nil
	}
	var customer model.MiniappCustomer
	wrongIdentity.Store(true)
	if err := (&RefundService{NewUpstreamRefundClient: factory}).CheckUpstreamRefund(context.Background(), f.tenantID, f.order.OrderNo); !errors.Is(err, ErrUpstreamRefundUnknown) {
		t.Fatalf("wrong third-party child accepted: %v", err)
	}
	wrongIdentity.Store(false)
	model.DB.First(&customer, link.MiniappCustomerID)
	miniapp := NewMiniappService()
	miniapp.NewUpstreamRefundClient = factory
	result, err := miniapp.ApplyXiaohongshuRefund(&customer, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "multi-user-refund", Reason: "游客主动退款"})
	if err != nil || result.Status != "processing" {
		t.Fatalf("customer refund=%+v %v", result, err)
	}
	money, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	refundSvc := xiaohongshuRefundServiceForTest(t, server)
	refundSvc.NewUpstreamRefundClient = factory
	runXiaohongshuRefundWorker(t, refundSvc, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, refundSvc, time.Now().Add(time.Minute))
	var payment model.Payment
	model.DB.First(&payment, f.payment.ID)
	if payment.RefundedAmountCents != f.refundAmount || cancellations.Load() != 1 || len(money.addRequest.Vouchers) != 2 {
		t.Fatalf("amount=%d cancellations=%d voucherCount=%d", payment.RefundedAmountCents, cancellations.Load(), len(money.addRequest.Vouchers))
	}
}

func TestXiaohongshuGroupVoucherAmountsRemainConserved(t *testing.T) {
	f := seedGroupedXhs(t, false)
	group, err := xiaohongshuTicketVouchers(model.DB, &f.ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&group[0]).Update("pay_amount_cents", 1).Error; err == nil {
		t.Fatal("sold voucher allocation was mutable")
	}
	a, b := int64(8950), int64(9950)
	group[0].PayAmountCents = &a
	group[1].PayAmountCents = &b
	details, err := xiaohongshuGroupRefundDetails(group, a+b)
	if err != nil || len(details) != 2 || details[0].RefundPrice != a || details[1].RefundPrice != b {
		t.Fatalf("discount allocation: %+v %v", details, err)
	}
	if _, err := xiaohongshuGroupRefundDetails(group, a+b-1); err == nil {
		t.Fatal("mismatched group refund accepted")
	}
}
