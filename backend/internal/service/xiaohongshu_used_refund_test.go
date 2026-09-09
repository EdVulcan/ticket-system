package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func verifyXiaohongshuRefundFixture(t *testing.T, f xiaohongshuRefundFixture) model.XiaohongshuVoucherVerification {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"code":0,"success":true,"data":{"access_token":"TEST","expire_in":7200}}`))
		case "/api/rmp/mp/deal/voucher/verify":
			_, _ = w.Write([]byte(`{"code":0,"success":true,"data":{"verify_id":"USED-REFUND-VERIFY"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var point model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", f.tenantID).First(&point).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewDeviceService(model.DB, &TicketService{})
	svc.NewXiaohongshuClient = func(app, secret, env string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: app, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	response, err := svc.VerifyDirect(DirectVerifyRequest{TenantID: f.tenantID, DeviceID: verificationDeviceID(t, f.tenantID, point.ID), CheckPointID: point.ID, RequestID: "mis-scan", RequestHash: "mis-scan", TicketCode: f.ticket.TicketCode})
	if err != nil || response == nil || response.Result != "allow" {
		t.Fatalf("verify fixture: %+v %v", response, err)
	}
	var saga model.XiaohongshuVoucherVerification
	if err := model.DB.Where("ticket_id = ?", f.ticket.ID).First(&saga).Error; err != nil {
		t.Fatal(err)
	}
	return saga
}

func TestXiaohongshuUsedRefundRequiresInitialAdminAndConfirmedVerification(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	saga := verifyXiaohongshuRefundFixture(t, f)
	admin := model.User{TenantID: f.tenantID, Username: "used-refund-admin", Password: "test", Role: "admin", IsInitialAdmin: true}
	ordinary := model.User{TenantID: f.tenantID, Username: "used-refund-ordinary", Password: "test", Role: "admin"}
	if err := model.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}
	svc := &RefundService{}
	apply := func(actor RefundActor, key string) (*model.Refund, error) {
		return svc.CreateMixedRefundAs(actor, f.order.OrderNo, key, f.order.TotalAmount, []string{f.ticket.TicketCode}, "工作人员误核销，确认退票")
	}
	for _, actor := range []RefundActor{{TenantID: f.tenantID}, {TenantID: f.tenantID, UserID: ordinary.ID}, {TenantID: f.tenantID + 999, UserID: admin.ID}} {
		if _, err := apply(actor, "denied"); err == nil {
			t.Fatal("unauthorized used refund accepted")
		}
	}
	if err := model.DB.Model(&saga).Updates(map[string]interface{}{"state": "manual_review", "manual_review_at": time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	actor := RefundActor{TenantID: f.tenantID, UserID: admin.ID}
	if _, err := apply(actor, "unconfirmed"); err == nil {
		t.Fatal("unconfirmed external verification accepted")
	}
	if err := model.DB.Model(&saga).Update("state", "local_completed").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("id = ?", saga.VoucherLinkID).Update("verify_id", "different-verification").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := apply(actor, "mismatched-verification"); err == nil {
		t.Fatal("mismatched external verification accepted")
	}
	if err := model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("id = ?", saga.VoucherLinkID).Update("verify_id", saga.VerifyID).Error; err != nil {
		t.Fatal(err)
	}
	r, err := apply(actor, "confirmed-used")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AuthorizedUsedRefund || r.AuthorizedBy != admin.ID || r.Status != "pending" {
		t.Fatalf("missing used refund authority: %+v", r)
	}
	replay, err := apply(actor, "confirmed-used")
	if err != nil || replay.ID != r.ID {
		t.Fatalf("used refund replay created another request: %+v %v", replay, err)
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	worker := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Second))
	var reserved model.Ticket
	var originalCheckIn model.CheckInRecord
	if err := model.DB.First(&reserved, f.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&originalCheckIn, saga.CheckInRecordID).Error; err != nil {
		t.Fatal(err)
	}
	if reserved.PendingRefundID != r.ID || reserved.CheckInCount == 0 || reserved.Status == "refunded" || originalCheckIn.ReversedAt != nil {
		t.Fatalf("add acceptance prematurely reversed admission: %+v %+v", reserved, originalCheckIn)
	}
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Minute))
	var stored model.Refund
	var ticket model.Ticket
	var checkIn model.CheckInRecord
	if err := model.DB.First(&stored, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&ticket, f.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&checkIn, saga.CheckInRecordID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || ticket.Status != "refunded" || ticket.CheckInCount == 0 || checkIn.ReversedAt == nil || checkIn.ReversalRefundID != r.ID {
		t.Fatalf("used refund did not preserve/reverse admission: refund=%s ticket=%+v checkin=%+v", stored.Status, ticket, checkIn)
	}
	if fake.addCalls.Load() != 1 {
		t.Fatal("refund submitted more than once")
	}
	if _, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(2*time.Minute), 20); err != nil {
		t.Fatal(err)
	}
	var retained model.XiaohongshuVoucherVerification
	if err := model.DB.First(&retained, saga.ID).Error; err != nil {
		t.Fatal(err)
	}
	if retained.VerifyID != saga.VerifyID || retained.CheckInRecordID != saga.CheckInRecordID || retained.State != "local_completed" {
		t.Fatalf("original verification evidence changed: %+v", retained)
	}
	if err := (&TicketService{}).Verify(f.ticket.TicketCode, originalCheckIn.CheckPointID, originalCheckIn.DeviceID, f.tenantID); err == nil {
		t.Fatal("refunded used ticket allowed another admission")
	}
	if fake.addCalls.Load() != 1 {
		t.Fatal("finished refund was submitted again")
	}
}

func TestXiaohongshuUsedRefundUnsuccessfulQueryPreservesAdmission(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		mismatch   bool
		wantRefund string
		reserved   bool
	}{
		{"pending", 1, false, "pending", true},
		{"provider_failure", 3, false, "failed", false},
		{"amount_mismatch", 2, true, "pending", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := seedXiaohongshuRefundFixture(t)
			saga := verifyXiaohongshuRefundFixture(t, f)
			admin := model.User{TenantID: f.tenantID, Username: "used-refund-admin", Password: "test", Role: "admin", IsInitialAdmin: true}
			if err := model.DB.Create(&admin).Error; err != nil {
				t.Fatal(err)
			}
			r, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: f.tenantID, UserID: admin.ID}, f.order.OrderNo, "used-refund-query", f.order.TotalAmount, []string{f.ticket.TicketCode}, "误核销退票")
			if err != nil {
				t.Fatal(err)
			}
			fake, server := newXiaohongshuRefundFake(t, tc.status, false)
			defer server.Close()
			fake.mismatch = tc.mismatch
			worker := xiaohongshuRefundServiceForTest(t, server)
			runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Second))
			runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Minute))
			var stored model.Refund
			var ticket model.Ticket
			var checkIn model.CheckInRecord
			var payment model.Payment
			for _, read := range []struct {
				dest interface{}
				id   uint
			}{{&stored, r.ID}, {&ticket, f.ticket.ID}, {&checkIn, saga.CheckInRecordID}, {&payment, f.payment.ID}} {
				if err := model.DB.First(read.dest, read.id).Error; err != nil {
					t.Fatal(err)
				}
			}
			if stored.Status != tc.wantRefund || (ticket.PendingRefundID == r.ID) != tc.reserved || ticket.CheckInCount == 0 || ticket.Status == "refunded" || checkIn.ReversedAt != nil || payment.RefundedAmountCents != 0 {
				t.Fatalf("unconfirmed refund changed consumed facts: refund=%s ticket=%+v checkin=%+v payment=%+v", stored.Status, ticket, checkIn, payment)
			}
			if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 {
				t.Fatal("unexpected provider call count")
			}
		})
	}
}
