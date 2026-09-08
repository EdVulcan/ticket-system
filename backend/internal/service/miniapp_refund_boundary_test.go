package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func TestMiniappRefundApplicationScopeAndConcurrentRequests(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	svc := NewMiniappService()
	otherTenant, _ := seedSellableProduct(t, "unlimited", 0)
	otherAccount := model.ChannelAccount{Code: "refund-boundary-other"}
	if err := (&ChannelService{}).CreateXiaohongshu(otherTenant, &otherAccount, "refund-boundary-app", "test-secret"); err != nil {
		t.Fatal(err)
	}
	otherCustomer := owner
	otherCustomer.Base = model.Base{}
	otherCustomer.TenantID, otherCustomer.ChannelAccountID = otherTenant, otherAccount.ID
	otherCustomer.OpenIDHash, otherCustomer.SessionTokenHash = hashMiniappValue("boundary-other-open"), hashMiniappValue("boundary-other-session")
	if err := model.DB.Create(&otherCustomer).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyXiaohongshuRefund(&otherCustomer, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "other-tenant", Reason: "test"}); err == nil {
		t.Fatal("valid other tenant customer accessed owner's order")
	}
	for _, change := range []func(*model.MiniappCustomer){func(c *model.MiniappCustomer) { c.TenantID++ }, func(c *model.MiniappCustomer) { c.ChannelAccountID++ }} {
		other := owner
		change(&other)
		if _, err := svc.ApplyXiaohongshuRefund(&other, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "scope", Reason: "test"}); err == nil {
			t.Fatal("forged tenant/account accepted")
		}
	}
	var group sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := svc.ApplyXiaohongshuRefund(&owner, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: fmt.Sprintf("concurrent-%d", i), Reason: "test"})
			results <- err
		}(i)
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successful applications=%d", successes)
	}
	var requests int64
	if err := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id=? AND order_no=?", f.tenantID, f.order.OrderNo).Count(&requests).Error; err != nil || requests != 1 {
		t.Fatalf("requests=%d err=%v", requests, err)
	}
	for _, table := range []interface{}{&model.Refund{}, &model.DigitalRefundTask{}, &model.XiaohongshuRefundOperation{}} {
		var count int64
		if err := model.DB.Model(table).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("automatic refund chain %T count=%d err=%v", table, count, err)
		}
	}
	req := model.AfterSaleRequest{TenantID: f.tenantID, OrderNo: f.order.OrderNo, Type: "refund", IdempotencyKey: "admin-duplicate", AmountCents: f.refundAmount, Reason: "test"}
	if err := (&AfterSaleService{}).Create(&req, []string{f.ticket.TicketCode}); err == nil {
		t.Fatal("admin created second active application")
	}
}

func TestMiniappRefundApplicationUsesSaleTimeRefundPolicy(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	if err := model.DB.Model(&model.OrderItem{}).Where("order_id=?", f.order.ID).Update("refund_type", "no_refund").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewMiniappService().ApplyXiaohongshuRefund(&owner, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "policy", Reason: "test"}); err == nil {
		t.Fatal("customer overrode sale-time no-refund policy")
	}
	var count int64
	if err := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected application writes=%d err=%v", count, err)
	}
}

func TestMiniappRefundApplicationConcurrentAdminRequest(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := NewMiniappService().ApplyXiaohongshuRefund(&owner, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "customer-admin-race", Reason: "test"})
		results <- err
	}()
	go func() {
		<-start
		req := model.AfterSaleRequest{TenantID: f.tenantID, OrderNo: f.order.OrderNo, Type: "refund", IdempotencyKey: "admin-customer-race", AmountCents: f.refundAmount, Reason: "test"}
		results <- (&AfterSaleService{}).Create(&req, []string{f.ticket.TicketCode})
	}()
	close(start)
	successes := 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				successes++
			} else if strings.Contains(strings.ToLower(err.Error()), "deadlock") || strings.Contains(err.Error(), "context deadline exceeded") {
				t.Fatalf("concurrent refund lock failure: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("customer/admin refund requests did not complete")
		}
	}
	if successes != 1 {
		t.Fatalf("customer/admin race successes=%d", successes)
	}
	var requests int64
	if err := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id=? AND order_no=?", f.tenantID, f.order.OrderNo).Count(&requests).Error; err != nil || requests != 1 {
		t.Fatalf("requests=%d err=%v", requests, err)
	}
	var tasks int64
	if err := model.DB.Model(&model.DigitalRefundTask{}).Where("tenant_id=?", f.tenantID).Count(&tasks).Error; err != nil || tasks > 1 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
}

func TestMiniappRefundApplicationReplaysAfterRefundCompleted(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	svc := NewMiniappService()
	input := MiniappRefundApplicationInput{ClientRequestID: "completed-replay", Reason: "test"}
	created, err := svc.ApplyXiaohongshuRefund(&owner, f.order.OrderNo, input)
	if err != nil {
		t.Fatal(err)
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	refundService := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, refundService, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, refundService, time.Now().Add(time.Minute))
	if err := (&AfterSaleService{}).ReconcileRefunds(); err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.ApplyXiaohongshuRefund(&owner, f.order.OrderNo, input)
	if err != nil || replayed.RequestNo != created.RequestNo || replayed.Status != "completed" {
		t.Fatalf("completed replay=%+v err=%v", replayed, err)
	}
	if fake.addCalls.Load() != 1 {
		t.Fatalf("provider add calls=%d", fake.addCalls.Load())
	}
}

func TestMiniappRefundApplicationRollsBackCompleteChain(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	// Fail the final request/refund link after the ticket reservation and task
	// are already written inside the transaction. This constraint exists only
	// in the process-isolated test database.
	if err := model.DB.Exec("ALTER TABLE after_sale_requests ADD CONSTRAINT test_customer_refund_atomic CHECK (refund_id = 0)").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := model.DB.Exec("ALTER TABLE after_sale_requests DROP CONSTRAINT IF EXISTS test_customer_refund_atomic").Error; err != nil {
			t.Error(err)
		}
	})
	if _, err := NewMiniappService().ApplyXiaohongshuRefund(&owner, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "atomic-rollback", Reason: "test"}); err == nil {
		t.Fatal("injected write failure was ignored")
	}
	for _, table := range []interface{}{&model.AfterSaleRequest{}, &model.AfterSaleEvent{}, &model.Refund{}, &model.DigitalRefundTask{}, &model.XiaohongshuRefundOperation{}} {
		var count int64
		if err := model.DB.Model(table).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rollback leaked %T count=%d err=%v", table, count, err)
		}
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, f.ticket.ID).Error; err != nil || ticket.PendingRefundID != 0 || ticket.Status != "unused" {
		t.Fatalf("rollback ticket=%+v err=%v", ticket, err)
	}
}

func TestMiniappRefundApplicationPausesDeviceVerification(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, f)
	if _, err := NewMiniappService().ApplyXiaohongshuRefund(&owner, f.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "refund-before-scan", Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	var device model.Device
	if err := model.DB.Where("tenant_id=?", f.tenantID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if device.CheckPointID == nil {
		t.Fatal("fixture device has no checkpoint")
	}
	var externalCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
		http.Error(w, "unexpected provider call", http.StatusInternalServerError)
	}))
	defer server.Close()
	svc := NewDeviceService(model.DB, &TicketService{})
	svc.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	result, err := svc.VerifyDirect(DirectVerifyRequest{TenantID: f.tenantID, DeviceID: device.ID, CheckPointID: *device.CheckPointID, RequestID: "scan-after-refund", RequestHash: "scan-after-refund-body", TicketCode: f.ticket.TicketCode})
	if err != nil || result.Result != "deny" || externalCalls.Load() != 0 {
		t.Fatalf("refund-reserved scan result=%+v err=%v externalCalls=%d", result, err, externalCalls.Load())
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, f.ticket.ID).Error; err != nil || ticket.CheckInCount != 0 || ticket.PendingRefundID == 0 {
		t.Fatalf("scan changed reserved ticket=%+v err=%v", ticket, err)
	}
}
