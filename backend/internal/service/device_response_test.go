package service

import (
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
)

func TestDenyResponseUsesDistinctLocalVoiceCodesForValidity(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		displayText string
		voiceCode   string
		reasonCode  string
	}{
		{name: "expired", err: ErrTicketExpired, displayText: "已过期", voiceCode: "expired", reasonCode: "expired"},
		{name: "not started", err: ErrTicketNotStarted, displayText: "未生效", voiceCode: "not_started", reasonCode: "not_started"},
		{name: "refunded", err: ErrTicketRefunded, displayText: "订单已退款，不能核销", voiceCode: "invalid", reasonCode: "refunded"},
		{name: "processing", err: fmt.Errorf("%w: concurrent verification", ErrTicketUnavailable), displayText: "核验处理中，请稍后重试", voiceCode: "manual_review", reasonCode: "processing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := denyResponse(tt.err)
			if resp.DisplayText != tt.displayText || resp.VoiceCode != tt.voiceCode || resp.ReasonCode != tt.reasonCode {
				t.Fatalf("response=%+v", resp)
			}
		})
	}
}

func TestRefundedTicketUsesDedicatedVerificationMessage(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&RefundService{}).CreateCashRefund(tenantID, order.OrderNo, "verify-refunded-ticket", order.TotalAmount, []string{ticket.TicketCode}, "visitor request"); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); !errors.Is(err, ErrTicketRefunded) {
		t.Fatalf("refunded ticket error=%v, want ErrTicketRefunded", err)
	}

	response, err := NewDeviceService(model.DB, &TicketService{}).VerifyDirect(DirectVerifyRequest{
		TenantID: tenantID, DeviceID: deviceID, CheckPointID: checkpoint.ID,
		RequestID: "verify-refunded-ticket", RequestHash: "verify-refunded-ticket-hash", TicketCode: ticket.TicketCode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Result != "deny" || response.DisplayText != "订单已退款，不能核销" {
		t.Fatalf("refunded ticket response=%+v", response)
	}
	if response.VoiceCode != "invalid" || response.VoiceFile != "invalid.mp3" {
		t.Fatalf("refunded ticket voice fallback=%+v", response)
	}
	var successful int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND result = ?", ticket.ID, "success").Count(&successful).Error; err != nil {
		t.Fatal(err)
	}
	if successful != 0 {
		t.Fatalf("refunded ticket created %d successful check-in records", successful)
	}
}

func TestXiaohongshuUnknownResponseUsesProcessingReasonCode(t *testing.T) {
	response := xiaohongshuVoucherUnknownResponse()
	if response.Code != 409 || response.Result != "deny" || response.ReasonCode != "processing" {
		t.Fatalf("unknown response=%+v", response)
	}
}
