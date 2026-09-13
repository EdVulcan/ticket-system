package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

func recoveryAdmin(t *testing.T, tenant uint) RefundActor {
	t.Helper()
	u := model.User{TenantID: tenant, Username: "recovery-admin", Password: "unused", Role: "admin", IsInitialAdmin: true}
	if err := model.DB.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return RefundActor{TenantID: tenant, UserID: u.ID}
}
func configureUpstreamFixture(t *testing.T, order *model.Order) {
	t.Helper()
	s := UpstreamSupplyService{}
	c, err := s.CreateConnection(order.TenantID, 0, "admin", UpstreamConnectionInput{Name: "ZYB", Provider: "zhiyoubao", Endpoint: "https://supplier.example/api", CorpCode: "corp", Username: "user", PrivateKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetConnectionStatus(order.TenantID, c.ID, 0, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProduct(order.TenantID, order.Items[0].ProductID, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: c.ID, ExternalProductCode: "GOODS"}); err != nil {
		t.Fatal(err)
	}
	order.ContactName = "测试联系人"
	order.ContactPhone = "13800000000"
}
func readyUpstreamFixture(t *testing.T, orderID uint, ticket *model.Ticket) {
	t.Helper()
	if err := model.DB.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ?", orderID).Updates(map[string]interface{}{"issue_status": "ready", "issue_attempted_at": time.Now(), "provider_order_code": "PROVIDER", "provider_sub_order_code": "SUB"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(ticket).Update("status", "unused").Error; err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamXhsFailedFundsRemainLockedAndReplaceOnce(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t, func(order *model.Order) { configureUpstreamFixture(t, order) })
	readyUpstreamFixture(t, f.order.ID, &f.ticket)
	actor := recoveryAdmin(t, f.tenantID)
	refund := createXiaohongshuRefund(t, f, "original-upstream-refund")
	money, server := newXiaohongshuRefundFake(t, 3, false)
	defer server.Close()
	s := xiaohongshuRefundServiceForTest(t, server)
	upstream := &upstreamRefundFake{}
	s.NewUpstreamRefundClient = func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return upstream, nil }
	runXiaohongshuRefundWorker(t, s, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, s, time.Now().Add(time.Minute))
	var held model.Ticket
	model.DB.First(&held, f.ticket.ID)
	if held.PendingRefundID != refund.ID {
		t.Fatal("failed payment refund unlocked cancelled supply")
	}
	var link model.XiaohongshuOrderLink
	model.DB.Where("order_id = ?", f.order.ID).First(&link)
	projection, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil || !projection.RefundPending || len(projection.TicketCodes) != 0 {
		t.Fatalf("failed funds exposed QR: %+v %v", projection, err)
	}
	if _, err := s.CreateMixedRefundAs(actor, f.order.OrderNo, "ordinary-second", f.order.TotalAmount, []string{held.TicketCode}, "retry"); err == nil {
		t.Fatal("ordinary route bypassed failed refund hold")
	}
	if _, err := s.RecoverUpstreamFunding(RefundActor{TenantID: f.tenantID}, refund.ID, "retry"); err == nil {
		t.Fatal("missing actor accepted")
	}
	var oldOp model.XiaohongshuRefundOperation
	model.DB.Where("refund_id = ?", refund.ID).First(&oldOp)
	if err := model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("ticket_id = ?", held.ID).Update("status", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverUpstreamFunding(actor, refund.ID, "invalid voucher should roll back"); err == nil {
		t.Fatal("invalid voucher recovery accepted")
	}
	model.DB.First(&held, held.ID)
	if held.PendingRefundID != refund.ID {
		t.Fatal("failed recovery leaked an unlocked ticket")
	}
	if err := model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("ticket_id = ?", held.ID).Update("status", 1).Error; err != nil {
		t.Fatal(err)
	}
	var results [2]*model.Refund
	var errs [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.RecoverUpstreamFunding(actor, refund.ID, "核对失败后恢复退款")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].ID == refund.ID || results[0].ID != results[1].ID {
		t.Fatal("concurrent recovery created multiple replacements")
	}
	model.DB.First(&held, held.ID)
	if held.PendingRefundID != results[0].ID {
		t.Fatal("ticket hold not atomically transferred")
	}
	var preserved model.XiaohongshuRefundOperation
	model.DB.First(&preserved, oldOp.ID)
	if preserved.State != "failed" || preserved.RequestPayloadCiphertext != oldOp.RequestPayloadCiphertext {
		t.Fatal("original platform operation changed")
	}
	money.status = 2
	runXiaohongshuRefundWorker(t, s, time.Now().Add(2*time.Minute))
	runXiaohongshuRefundWorker(t, s, time.Now().Add(3*time.Minute))
	var paid model.Payment
	model.DB.First(&paid, f.payment.ID)
	model.DB.First(&held, held.ID)
	if paid.RefundedAmountCents != f.refundAmount || held.Status != "refunded" || upstream.cancels != 1 || money.addCalls.Load() != 2 {
		t.Fatalf("refund=%d status=%s cancellations=%d adds=%d", paid.RefundedAmountCents, held.Status, upstream.cancels, money.addCalls.Load())
	}
	if again, err := s.RecoverUpstreamFunding(actor, refund.ID, "same recovery"); err != nil || again.ID != results[0].ID {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
}

func TestUpstreamUnsentXhsOrderCanRefundWithoutIssuance(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t, func(order *model.Order) { configureUpstreamFixture(t, order) })
	if f.ticket.Status != "pending_provider" {
		t.Fatal("expected awaiting supplier issuance")
	}
	refund := createXiaohongshuRefund(t, f, "unsent-refund")
	_, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	s := xiaohongshuRefundServiceForTest(t, server)
	s.NewUpstreamRefundClient = func(*model.UpstreamConnection) (UpstreamRefundClient, error) {
		t.Fatal("unsent order must not call supplier")
		return nil, nil
	}
	runXiaohongshuRefundWorker(t, s, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, s, time.Now().Add(time.Minute))
	var saved model.Refund
	model.DB.First(&saved, refund.ID)
	if saved.Status != "succeeded" {
		t.Fatalf("refund=%s", saved.Status)
	}
}

type recoveryMoneyProvider struct {
	failed bool
	calls  int
}

func (p *recoveryMoneyProvider) Process(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error) {
	p.calls++
	if p.failed {
		return RefundProviderResult{Status: "failed"}, nil
	}
	return RefundProviderResult{Status: "succeeded"}, nil
}

func TestUpstreamDigitalRecoveryKeepsOriginalTask(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			order := seedUpstreamWorkerOrder(t, "https://supplier.example/api")
			if err := (&OrderService{}).MarkAsPaid(order.OrderNo, order.TenantID); err != nil {
				t.Fatal(err)
			}
			payment := model.Payment{TenantID: order.TenantID, OrderNo: order.OrderNo, PaymentNo: "RECOVERY-PAY", IdempotencyKey: "paid", Method: "alipay", Status: "paid", Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount)}
			if err := model.DB.Create(&payment).Error; err != nil {
				t.Fatal(err)
			}
			ticket := order.Items[0].Tickets[0]
			readyUpstreamFixture(t, order.ID, &ticket)
			actor := recoveryAdmin(t, order.TenantID)
			money := &recoveryMoneyProvider{failed: true}
			vendor := &upstreamRefundFake{}
			s := &RefundService{Provider: money, NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return vendor, nil }}
			var refund *model.Refund
			var err error
			if mixed {
				refund, err = s.CreateMixedRefundAs(actor, order.OrderNo, "refund", order.TotalAmount, []string{ticket.TicketCode}, "取消")
			} else {
				refund, err = s.CreateDigitalRefundAs(actor, order.OrderNo, "refund", order.TotalAmount, []string{ticket.TicketCode}, "取消")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Minute), 1); err != nil {
				t.Fatal(err)
			}
			model.DB.First(&ticket, ticket.ID)
			if ticket.PendingRefundID != refund.ID {
				t.Fatal("hold lost")
			}
			if mixed {
				if err = s.ResolveMixedRefundGroup(order.TenantID, refund.ID, actor.UserID, "admin", "close_failed", "close"); err == nil {
					t.Fatal("cancelled supply reopened through mixed group close")
				}
			}
			if _, err = s.RecoverUpstreamFunding(actor, refund.ID, "已核对，恢复原任务"); err != nil {
				t.Fatal(err)
			}
			money.failed = false
			if _, err = s.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(2*time.Minute), 1); err != nil {
				t.Fatal(err)
			}
			model.DB.First(&ticket, ticket.ID)
			if ticket.Status != "refunded" || vendor.cancels != 1 {
				t.Fatalf("ticket=%s cancel=%d", ticket.Status, vendor.cancels)
			}
		})
	}
}

func TestUpstreamIssuanceRecoveryChecksOriginalOrder(t *testing.T) {
	mode := "error"
	sends := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		if strings.Contains(xml, "SEND_CODE_REQ") {
			sends++
			http.Error(w, "not issuing in this check", 503)
			return
		}
		if mode == "error" {
			http.Error(w, "unavailable", 503)
			return
		}
		description := "订单不存在"
		if mode == "invalid" {
			description = "外部订单号格式错误"
		}
		transaction := "QUERY_ORDER_NEW_RES"
		if mode == "notfound_empty" {
			transaction = ""
		} // Existing ZYB error samples omit transactionName.
		fmt.Fprintf(w, `<PWBResponse><transactionName>%s</transactionName><code>6</code><description>%s</description></PWBResponse>`, transaction, description)
	}))
	defer server.Close()
	order := seedUpstreamWorkerOrder(t, server.URL)
	actor := recoveryAdmin(t, order.TenantID)
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, order.TenantID); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	if err := model.DB.Model(&snapshot).Update("issue_attempted_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	w := &UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: c.CorpCode, Username: c.Username, PrivateKey: "key"}, HTTP: server.Client()}, nil
	}}
	if err := w.RecoverIssuance(context.Background(), order.TenantID, 0, order.OrderNo, "check", true); err == nil {
		t.Fatal("missing actor accepted")
	}
	for _, state := range []string{"error", "invalid"} {
		mode = state
		if err := w.RecoverIssuance(context.Background(), order.TenantID, actor.UserID, order.OrderNo, "check", true); err == nil {
			t.Fatal("unknown/not-found-like error authorized resend")
		}
	}
	mode = "notfound_empty"
	if err := w.RecoverIssuance(context.Background(), order.TenantID, actor.UserID, order.OrderNo, "check", false); err == nil {
		t.Fatal("resend without explicit confirmation")
	}
	if err := w.RecoverIssuance(context.Background(), order.TenantID, actor.UserID, order.OrderNo, "确认供方未成单", true); err != nil {
		t.Fatal(err)
	}
	var refreshed model.OrderItemSupplySnapshot
	model.DB.First(&refreshed, snapshot.ID)
	if refreshed.IssueAttemptedAt != nil || sends != 0 {
		t.Fatal("recovery changed order or sent outside worker")
	}
	if _, err := w.ProcessTasks(context.Background(), time.Now().Add(time.Second), 1); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("resend count=%d", sends)
	}
	if _, err := w.ProcessTasks(context.Background(), time.Now().Add(2*time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("automatic loop sent again")
	}
}

func TestUpstreamDraftWindowAndManualReschedule(t *testing.T) {
	order, ticket := seedReadyUpstreamRefund(t)
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	s := UpstreamSupplyService{}
	productID := order.Items[0].ProductID
	for _, change := range []ProductSupplyInput{{UpstreamConnectionID: snapshot.ConnectionID, ExternalProductCode: "DRAFT"}, {Enabled: true, UpstreamConnectionID: snapshot.ConnectionID, ExternalProductCode: "ACTIVE"}, {UpstreamConnectionID: snapshot.ConnectionID, ExternalProductCode: "LATEST"}} {
		if _, err := s.SetProduct(order.TenantID, productID, 0, "admin", change); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.GetProduct(order.TenantID, productID)
	if err != nil || view.ExternalProductCode != "LATEST" {
		t.Fatalf("draft=%+v %v", view, err)
	}
	date := startOfDay(time.Now().AddDate(0, 0, 1))
	request := model.AfterSaleRequest{TenantID: order.TenantID, OrderNo: order.OrderNo, Type: "reschedule", IdempotencyKey: "manual-upstream-date", TargetDate: &date, OperatorID: 1, Reason: "人工同步两边日期"}
	afterSale := &AfterSaleService{}
	if err = afterSale.Create(&request, []string{ticket.TicketCode}); err != nil {
		t.Fatal(err)
	}
	if _, err = afterSale.Approve(order.TenantID, request.ID, 2, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err = afterSale.Execute(order.TenantID, request.ID, 2); err != nil {
		t.Fatal(err)
	}
	var after model.OrderItemSupplySnapshot
	model.DB.First(&after, snapshot.ID)
	if !reflect.DeepEqual(snapshot, after) {
		t.Fatal("manual date change rewrote supply identity")
	}
	model.DB.First(&ticket, ticket.ID)
	if ticket.TicketCode != order.Items[0].Tickets[0].TicketCode {
		t.Fatal("manual reschedule changed shared code")
	}
	if _, err = s.SetProduct(order.TenantID, productID, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: snapshot.ConnectionID, ExternalProductCode: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if err = model.DB.Model(&model.Product{}).Where("id = ?", productID).Update("type", "offline").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProduct(order.TenantID, productID, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: snapshot.ConnectionID, ExternalProductCode: "WINDOW"}); err == nil {
		t.Fatal("window upstream enabled")
	}
	window := model.Order{TenantID: order.TenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err = (&OrderService{}).Create(&window); err != nil {
		t.Fatal(err)
	}
	var windowSupply model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", window.ID).First(&windowSupply)
	if windowSupply.Mode != "local" || window.ContactName != "" || window.ContactPhone != "" {
		t.Fatal("window was not local anonymous sale")
	}
}
