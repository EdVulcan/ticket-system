package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestXiaohongshuRefundPolicyOverridePreservesSnapshotAndChannelConfirmation(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	if err := model.DB.Model(&model.OrderItem{}).Where("order_id = ?", f.order.ID).Update("refund_type", "no_refund").Error; err != nil {
		t.Fatal(err)
	}
	initial := model.User{TenantID: f.tenantID, Username: "xhs-policy-initial", Password: "test", Role: "super_admin", IsInitialAdmin: true}
	ordinary := model.User{TenantID: f.tenantID, Username: "xhs-policy-ordinary", Password: "test", Role: "admin"}
	for _, user := range []*model.User{&initial, &ordinary} {
		if err := model.DB.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &RefundService{}
	for _, actor := range []RefundActor{
		{TenantID: f.tenantID, UserID: ordinary.ID, OverrideRefundPolicy: true},
		{TenantID: f.tenantID, UserID: initial.ID},
	} {
		if _, err := service.CreateMixedRefundAs(actor, f.order.OrderNo, "xhs-policy-rejected", f.order.TotalAmount, []string{f.ticket.TicketCode}, "政策设置错误"); err == nil {
			t.Fatal("policy override accepted without both initial-admin authority and explicit override")
		}
	}
	actor := RefundActor{TenantID: f.tenantID, UserID: initial.ID, OverrideRefundPolicy: true}
	refund, err := service.CreateMixedRefundAs(actor, f.order.OrderNo, "xhs-policy-approved", f.order.TotalAmount, []string{f.ticket.TicketCode}, "政策设置错误")
	if err != nil {
		t.Fatal(err)
	}
	if !refund.AuthorizedPolicyOverride || refund.AuthorizedBy != initial.ID || refund.Status != "pending" {
		t.Fatalf("override must be audited and await channel confirmation: %+v", refund)
	}
	replay, err := service.CreateMixedRefundAs(actor, f.order.OrderNo, "xhs-policy-approved", f.order.TotalAmount, []string{f.ticket.TicketCode}, "政策设置错误")
	if err != nil || replay.ID != refund.ID {
		t.Fatalf("override retry not idempotent: %v", err)
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	worker := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Minute))
	if err := model.DB.First(refund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", f.order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" || fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 || item.RefundType != "no_refund" {
		t.Fatalf("override must use channel confirmation without rewriting sale policy: status=%s policy=%s add=%d get=%d", refund.Status, item.RefundType, fake.addCalls.Load(), fake.getCalls.Load())
	}
}
