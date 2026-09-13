package service

import (
	"context"
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

type upstreamRefundFake struct {
	cancels int
	pending bool
	used    bool
	unknown bool
}

func (f *upstreamRefundFake) QueryOrder(context.Context, string) (*zyb.QueryOrderResult, []byte, error) {
	if f.unknown {
		return nil, nil, errors.New("network failed")
	}
	checked := "0"
	if f.used {
		checked = "1"
	}
	return &zyb.QueryOrderResult{ProviderOrderCode: "PROVIDER", Tickets: []zyb.QueryOrderTicket{{ProviderSubOrderCode: "SUB", GoodsCode: "GOODS", Quantity: "1", ReturnedQuantity: "0", CheckedQuantity: checked}}}, nil, nil
}
func (f *upstreamRefundFake) CancelOrder(context.Context, string) (*zyb.CancelOrderResult, []byte, error) {
	f.cancels++
	return &zyb.CancelOrderResult{RetreatBatchNo: "BATCH"}, nil, nil
}
func (f *upstreamRefundFake) QueryRefund(context.Context, string) (*zyb.RefundResult, []byte, error) {
	return &zyb.RefundResult{Completed: !f.pending, Pending: f.pending}, nil, nil
}

func seedReadyUpstreamRefund(t *testing.T) (model.Order, model.Ticket) {
	t.Helper()
	order := seedUpstreamWorkerOrder(t, "https://supplier.example/api")
	p := model.Payment{OrderNo: order.OrderNo, Method: "cash", IdempotencyKey: "cash-ready"}
	if err := (&PaymentService{}).CreatePayment(order.TenantID, &p); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	if err := model.DB.Model(&snapshot).Updates(map[string]interface{}{"issue_status": "ready", "issue_attempted_at": time.Now(), "provider_order_code": "PROVIDER", "provider_sub_order_code": "SUB"}).Error; err != nil {
		t.Fatal(err)
	}
	ticket := order.Items[0].Tickets[0]
	if err := model.DB.Model(&ticket).Update("status", "unused").Error; err != nil {
		t.Fatal(err)
	}
	ticket.Status = "unused"
	return order, ticket
}

func TestUpstreamRefundWaitsForCancelAndLocksVerification(t *testing.T) {
	order, ticket := seedReadyUpstreamRefund(t)
	fake := &upstreamRefundFake{pending: true}
	service := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return fake, nil }}
	if err := service.preflightUpstreamRefund(context.Background(), order.TenantID, order.OrderNo); err != nil {
		t.Fatal(err)
	}
	if fake.cancels != 0 {
		t.Fatal("read-only preflight cancelled supplier order")
	}
	refund, err := service.CreateCashRefund(order.TenantID, order.OrderNo, "refund", 99.50, []string{ticket.TicketCode}, "游客取消")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "pending" {
		t.Fatal("cash refund finalized before upstream cancellation")
	}
	var device model.Device
	model.DB.Where("tenant_id = ?", order.TenantID).First(&device)
	if err := (&TicketService{}).Verify(ticket.TicketCode, *device.CheckPointID, device.ID, order.TenantID); err == nil {
		t.Fatal("ticket verified during refund")
	}
	now := time.Now()
	if _, err = service.ProcessDigitalRefundTasks(context.Background(), now, 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	if refund.Status != "pending" || fake.cancels != 1 {
		t.Fatalf("refund=%s cancel=%d", refund.Status, fake.cancels)
	}
	fake.pending = false
	fake.unknown = true // Once the cancel batch is known, order queries cannot block completion.
	if _, err = service.ProcessDigitalRefundTasks(context.Background(), now.Add(10*time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	model.DB.First(&ticket, ticket.ID)
	if refund.Status != "succeeded" || ticket.Status != "refunded" || fake.cancels != 1 {
		t.Fatalf("refund=%s ticket=%s cancel=%d", refund.Status, ticket.Status, fake.cancels)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, *device.CheckPointID, device.ID, order.TenantID); !errors.Is(err, ErrTicketRefunded) {
		t.Fatalf("refunded scan: %v", err)
	}
}

func TestUpstreamRefundConflictRequiresInitialAdminAndResumesSameTask(t *testing.T) {
	order, ticket := seedReadyUpstreamRefund(t)
	fake := &upstreamRefundFake{used: true}
	service := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return fake, nil }}
	if err := service.preflightUpstreamRefund(context.Background(), order.TenantID, order.OrderNo); !errors.Is(err, ErrUpstreamRefundUsed) {
		t.Fatalf("preflight=%v", err)
	}
	refund, err := service.CreateCashRefund(order.TenantID, order.OrderNo, "used-refund", 99.50, []string{ticket.TicketCode}, "状态冲突")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProcessDigitalRefundTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var task model.DigitalRefundTask
	model.DB.Where("refund_id = ?", refund.ID).First(&task)
	if task.Status != "manual_review" || fake.cancels != 0 {
		t.Fatalf("task=%s cancels=%d", task.Status, fake.cancels)
	}
	if err = service.ConfirmUpstreamRefund(RefundActor{TenantID: order.TenantID}, refund.ID, "继续退款"); err == nil {
		t.Fatal("anonymous confirmation accepted")
	}
	user := model.User{TenantID: order.TenantID, Username: "upstream-admin", Password: "unused", Role: "admin", IsInitialAdmin: true}
	if err = model.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err = service.ConfirmUpstreamRefund(RefundActor{TenantID: order.TenantID, UserID: user.ID}, refund.ID, "已确认现场误核销，继续退"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	if refund.Status != "succeeded" {
		t.Fatalf("refund=%s", refund.Status)
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	if snapshot.CancelStatus != "override" || snapshot.ProviderFirstUsedAt != nil {
		t.Fatal("special refund forged supplier cancellation or first-use time")
	}
}

func TestUpstreamMixedCashDoesNotCompleteBeforeCancellation(t *testing.T) {
	order, ticket := seedReadyUpstreamRefund(t)
	fake := &upstreamRefundFake{pending: true}
	svc := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return fake, nil }}
	refund, err := svc.CreateMixedRefund(order.TenantID, order.OrderNo, "mixed", 99.50, []string{ticket.TicketCode}, "取消")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "group_pending" {
		t.Fatal("mixed cash group completed early")
	}
	if _, err = svc.ProcessDigitalRefundTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	if refund.Status != "group_pending" {
		t.Fatal("pending upstream cancelled money early")
	}
	fake.pending = false
	if _, err = svc.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(10*time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	model.DB.First(refund, refund.ID)
	if refund.Status != "group_succeeded" {
		var tasks []model.DigitalRefundTask
		model.DB.Where("tenant_id = ?", order.TenantID).Find(&tasks)
		var allocations []model.Refund
		model.DB.Where("parent_refund_id = ?", refund.ID).Find(&allocations)
		t.Fatalf("group status=%s tasks=%+v allocations=%+v", refund.Status, tasks, allocations)
	}
}
