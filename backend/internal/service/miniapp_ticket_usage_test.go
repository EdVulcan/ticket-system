package service

import (
	"context"
	"testing"
	"ticket-backend/internal/model"
)

func TestMiniappTicketUsagePreservesSharedCodeAndRepeatRights(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	var customer model.MiniappCustomer
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ?", f.tenantID, f.account.ID).First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMiniappService()
	other := customer
	other.ID++
	if _, err := service.GetXiaohongshuOrder(context.Background(), &other, f.order.OrderNo); err == nil {
		t.Fatal("another customer read ticket usage")
	}
	for _, state := range []struct {
		status string
		count  int
	}{{"unused", 0}, {"active", 1}, {"used", 2}} {
		if err := model.DB.Model(&f.ticket).Updates(map[string]interface{}{"status": state.status, "check_in_count": state.count}).Error; err != nil {
			t.Fatal(err)
		}
		result, err := service.GetXiaohongshuOrder(context.Background(), &customer, f.order.OrderNo)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tickets) != 1 || result.Tickets[0].Code != f.ticket.TicketCode || result.Tickets[0].Status != state.status || result.Tickets[0].CheckInCount != state.count || len(result.TicketCodes) != 1 || result.TicketCodes[0] != f.ticket.TicketCode {
			t.Fatalf("ticket projection changed rights: %+v", result)
		}
	}
}
