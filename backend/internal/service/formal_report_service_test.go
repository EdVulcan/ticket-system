package service

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestWindowOrderExposesOriginalCashierDeviceAndShift(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var product model.Product
	if err := model.DB.Select("id", "scenic_area_id").First(&product, productID).Error; err != nil {
		t.Fatal(err)
	}
	staff := model.Staff{TenantID: tenantID, Name: "窗口售票员", JobNumber: "SELLER-001", Password: "test", Roles: "seller", Status: "active"}
	if err := model.DB.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	device := model.Device{TenantID: tenantID, ScenicAreaID: product.ScenicAreaID, Name: "一号售票终端", SerialNumber: "POS-ATTRIBUTION-001", Type: "pos", Status: "online"}
	if err := model.DB.Create(&device).Error; err != nil {
		t.Fatal(err)
	}
	shift := model.POSShift{
		TenantID: tenantID, ScenicAreaID: product.ScenicAreaID, DeviceID: device.ID, OperatorID: staff.ID,
		ShiftNo: fmt.Sprintf("SHIFT-ATTRIBUTION-%d", time.Now().UnixNano()), Status: "open", OpenedAt: time.Now(),
	}
	if err := model.DB.Create(&shift).Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		OrderNo: order.OrderNo, Method: "cash", ShiftID: shift.ID, DeviceID: device.ID, OperatorID: staff.ID,
		TenderedCents: int64(order.TotalAmount*100 + 0.5), IdempotencyKey: "sale-attribution-payment",
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &payment); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&payment).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}

	orders, total, err := (&OrderService{}).List(1, 10, tenantID, "", "window", "", "", order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(orders) != 1 {
		t.Fatalf("window order list total=%d rows=%d", total, len(orders))
	}
	got := orders[0]
	if got.SaleOperatorID != staff.ID || got.SaleOperatorName != staff.Name || got.SaleOperatorJobNumber != staff.JobNumber ||
		got.SaleDeviceID != device.ID || got.SaleDeviceName != device.Name || got.SaleDeviceSerial != device.SerialNumber ||
		got.SaleShiftID != shift.ID || got.SaleShiftNo != shift.ShiftNo {
		t.Fatalf("sale attribution=%+v", got)
	}
	detail, err := (&OrderService{}).GetDetail(order.OrderNo, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Order.SaleOperatorID != staff.ID || detail.Order.SaleDeviceID != device.ID || detail.Order.SaleShiftID != shift.ID {
		t.Fatalf("detail sale attribution=%+v", detail.Order)
	}
}

func TestRefundPolicyOverrideRequiresInitialSupplierAdminAndReason(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", productID).Update("refund_type", "no_refund").Error; err != nil {
		t.Fatal(err)
	}
	initial := model.User{TenantID: tenantID, Username: "policy-initial", Password: "test", Role: "super_admin", IsInitialAdmin: true}
	ordinary := model.User{TenantID: tenantID, Username: "policy-ordinary", Password: "test", Role: "admin"}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}
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
	refunds := &RefundService{}
	_, err := refunds.CreateMixedRefundAs(RefundActor{TenantID: tenantID, UserID: ordinary.ID, OverrideRefundPolicy: true}, order.OrderNo, "policy-ordinary", order.TotalAmount, []string{ticket.TicketCode}, "manager request")
	if err == nil || !strings.Contains(err.Error(), "initial administrator") {
		t.Fatalf("ordinary policy override error=%v", err)
	}
	_, err = refunds.CreateMixedRefundAs(RefundActor{TenantID: tenantID, UserID: initial.ID, OverrideRefundPolicy: true}, order.OrderNo, "policy-no-reason", order.TotalAmount, []string{ticket.TicketCode}, "")
	if err == nil || !strings.Contains(err.Error(), "requires a reason") {
		t.Fatalf("blank-reason policy override error=%v", err)
	}
	refund, err := refunds.CreateMixedRefundAs(RefundActor{TenantID: tenantID, UserID: initial.ID, OverrideRefundPolicy: true}, order.OrderNo, "policy-approved", order.TotalAmount, []string{ticket.TicketCode}, "现场核实后例外退款")
	if err != nil {
		t.Fatal(err)
	}
	if !refund.AuthorizedPolicyOverride || refund.AuthorizedBy != initial.ID || refund.Status != "group_succeeded" {
		t.Fatalf("policy override audit not persisted: %+v", refund)
	}
}

func TestUsedTicketRefundRequiresInitialSupplierAdminAndRemovesVerificationFact(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)

	initialAdmin := model.User{TenantID: tenantID, Username: "initial-admin", Password: "test", Role: "admin", IsInitialAdmin: true}
	ordinaryAdmin := model.User{TenantID: tenantID, Username: "ordinary-admin", Password: "test", Role: "admin"}
	if err := model.DB.Create(&initialAdmin).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&ordinaryAdmin).Error; err != nil {
		t.Fatal(err)
	}

	order := model.Order{TenantID: tenantID, Channel: "online", ContactName: "Visitor", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", OperatorID: ordinaryAdmin.ID}
	if err := (&PaymentService{}).CreatePayment(tenantID, &payment); err != nil {
		t.Fatal(err)
	}

	var ticket model.Ticket
	var checkpoint model.CheckPoint
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, verificationDeviceID(t, tenantID, checkpoint.ID), tenantID); err != nil {
		t.Fatal(err)
	}
	group := model.TourGroup{
		TenantID: tenantID, SupplierTenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID,
		SalesOrderID: order.ID, GroupNo: "REFUND-CREDIT-GROUP", Name: "Refund credit group",
		VisitDate: time.Now(), ExpectedCount: 1, Status: "entered", ContractAmountCents: 9950,
		CreditUsedCents: 9950, SettlementStatus: "open",
	}
	if err := model.DB.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	today := time.Now().Format("2006-01-02")
	filter := FormalReportFilter{StartDate: today, EndDate: today}
	summary, err := (&ReportService{}).GetVerificationSummary(tenantID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 || summary[0].VerifiedCount != 1 || summary[0].IncomeCents != 9950 {
		t.Fatalf("verification summary before refund = %+v, want one admission and 9950 cents", summary)
	}
	details, total, err := (&ReportService{}).GetVerificationDetails(tenantID, filter, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(details) != 1 || details[0].TicketCode != ticket.TicketCode {
		t.Fatalf("verification details before refund total=%d rows=%+v", total, details)
	}

	refunds := &RefundService{}
	if _, err := refunds.CreateCashRefundAs(RefundActor{TenantID: tenantID, UserID: ordinaryAdmin.ID}, order.OrderNo, "used-refund-denied", order.TotalAmount, []string{ticket.TicketCode}, "visitor request"); err == nil {
		t.Fatal("ordinary administrator refunded a used ticket")
	}
	refund, err := refunds.CreateCashRefundAs(RefundActor{TenantID: tenantID, UserID: initialAdmin.ID}, order.OrderNo, "used-refund-approved", order.TotalAmount, []string{ticket.TicketCode}, "visitor request")
	if err != nil {
		t.Fatal(err)
	}
	if !refund.AuthorizedUsedRefund || refund.AuthorizedBy != initialAdmin.ID || refund.Status != "succeeded" {
		t.Fatalf("used refund authorization not persisted: %+v", refund)
	}

	var storedTicket model.Ticket
	var storedOrder model.Order
	var checkIn model.CheckInRecord
	if err := model.DB.First(&storedTicket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&storedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("ticket_id = ? AND result = ?", ticket.ID, "success").First(&checkIn).Error; err != nil {
		t.Fatal(err)
	}
	if storedTicket.Status != "refunded" || storedOrder.Status != "refunded" {
		t.Fatalf("ticket/order status=%s/%s, want refunded/refunded", storedTicket.Status, storedOrder.Status)
	}
	if checkIn.ReversedAt == nil || checkIn.ReversedBy != initialAdmin.ID || checkIn.ReversalRefundID != refund.ID {
		t.Fatalf("check-in reversal audit = %+v", checkIn)
	}
	if err := model.DB.First(&group, group.ID).Error; err != nil || group.CreditUsedCents != 0 {
		t.Fatalf("team credit was not released immediately: %+v err=%v", group, err)
	}

	summary, err = (&ReportService{}).GetVerificationSummary(tenantID, filter)
	if err != nil {
		t.Fatal(err)
	}
	details, total, err = (&ReportService{}).GetVerificationDetails(tenantID, filter, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 0 || total != 0 || len(details) != 0 {
		t.Fatalf("refunded verification remained visible: summary=%+v total=%d details=%+v", summary, total, details)
	}

	daily, err := (&ReportService{}).GetDailyReport(tenantID, today, today)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily.CheckIns) != 0 {
		t.Fatalf("refunded verification remained in daily check-ins: %+v", daily.CheckIns)
	}
	business, businessTotal, err := (&ReportService{}).GetBusinessDetails(tenantID, filter, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	refundFacts := 0
	for _, row := range business {
		if row.FactType == "refund" {
			refundFacts++
		}
	}
	if businessTotal != 2 || refundFacts != 1 {
		t.Fatalf("business facts total=%d refund facts=%d rows=%+v", businessTotal, refundFacts, business)
	}
}
