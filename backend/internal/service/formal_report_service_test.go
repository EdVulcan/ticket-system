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

func TestLaterUsedTicketRefundRestatesOriginalVerificationPeriod(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	initialAdmin := model.User{
		TenantID: tenantID, Username: "period-restatement-admin", Password: "test",
		Role: "admin", IsInitialAdmin: true,
	}
	if err := model.DB.Create(&initialAdmin).Error; err != nil {
		t.Fatal(err)
	}

	order := model.Order{
		TenantID: tenantID, Channel: "online", ContactName: "Visitor", ContactPhone: "13800138000",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 10}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{
		OrderNo: order.OrderNo, Method: "cash", OperatorID: initialAdmin.ID,
	}); err != nil {
		t.Fatal(err)
	}

	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 10 {
		t.Fatalf("tickets=%d, want 10", len(tickets))
	}
	for i := range tickets {
		if err := (&TicketService{}).Verify(tickets[i].TicketCode, checkpoint.ID, deviceID, tenantID); err != nil {
			t.Fatal(err)
		}
	}

	// Put the successful admissions in the prior accounting period. The refund
	// remains in the current period, so this exercises a true cross-period
	// restatement instead of a same-day query refresh.
	now := time.Now()
	originalPeriod := now.AddDate(0, -1, 0)
	originalCheckInAt := time.Date(originalPeriod.Year(), originalPeriod.Month(), 10, 12, 0, 0, 0, now.Location())
	if err := model.DB.Model(&model.CheckInRecord{}).
		Where("tenant_id = ? AND ticket_id IN ?", tenantID, []uint{
			tickets[0].ID, tickets[1].ID, tickets[2].ID, tickets[3].ID, tickets[4].ID,
			tickets[5].ID, tickets[6].ID, tickets[7].ID, tickets[8].ID, tickets[9].ID,
		}).Update("check_in_time", originalCheckInAt).Error; err != nil {
		t.Fatal(err)
	}
	originalStart := time.Date(originalPeriod.Year(), originalPeriod.Month(), 1, 0, 0, 0, 0, now.Location())
	originalEnd := originalStart.AddDate(0, 1, -1)
	originalFilter := FormalReportFilter{
		StartDate: originalStart.Format("2006-01-02"),
		EndDate:   originalEnd.Format("2006-01-02"),
	}
	assertVerifiedCount := func(want int64) {
		t.Helper()
		rows, err := (&ReportService{}).GetVerificationSummary(tenantID, originalFilter)
		if err != nil {
			t.Fatal(err)
		}
		var got, incomeCents int64
		for _, row := range rows {
			got += row.VerifiedCount
			incomeCents += row.IncomeCents
		}
		if got != want {
			t.Fatalf("original-period verified count=%d, want %d; rows=%+v", got, want, rows)
		}
		wantIncomeCents := want * 9950
		if incomeCents != wantIncomeCents {
			t.Fatalf("original-period verification income=%d, want %d; rows=%+v", incomeCents, wantIncomeCents, rows)
		}
	}
	assertVerifiedCount(10)

	refund, err := (&RefundService{}).CreateCashRefundAs(
		RefundActor{TenantID: tenantID, UserID: initialAdmin.ID},
		order.OrderNo,
		"cross-period-used-refund",
		order.Items[0].Price*2,
		[]string{tickets[0].TicketCode, tickets[1].TicketCode},
		"cross-period correction",
	)
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" {
		t.Fatalf("refund status=%s, want succeeded", refund.Status)
	}

	// The later refund rewrites the original verification-income fact: the
	// prior period becomes 8 admissions. It must not create verification income
	// (positive or negative) in the refund-action period.
	assertVerifiedCount(8)
	currentStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	currentEnd := currentStart.AddDate(0, 1, -1)
	currentRows, err := (&ReportService{}).GetVerificationSummary(tenantID, FormalReportFilter{
		StartDate: currentStart.Format("2006-01-02"),
		EndDate:   currentEnd.Format("2006-01-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(currentRows) != 0 {
		t.Fatalf("refund-action period contains verification income: %+v", currentRows)
	}
}

func TestSalesReportsUseOriginalPaymentPeriodAfterLaterRefund(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{
		TenantID: tenantID, Channel: "online", ContactName: "Visitor", ContactPhone: "13800138000",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 1}},
	}
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

	now := time.Now()
	originalPeriod := now.AddDate(0, -1, 0)
	originalPaidAt := time.Date(originalPeriod.Year(), originalPeriod.Month(), 10, 12, 0, 0, 0, now.Location())
	var payment model.Payment
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&payment).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&payment).Updates(map[string]interface{}{"paid_at": originalPaidAt, "created_at": originalPaidAt}).Error; err != nil {
		t.Fatal(err)
	}
	refund, err := (&RefundService{}).CreateCashRefund(tenantID, order.OrderNo, "sales-report-cross-period", order.TotalAmount, []string{ticket.TicketCode}, "cross-period sales report")
	if err != nil || refund.Status != "succeeded" {
		t.Fatalf("refund=%+v err=%v", refund, err)
	}
	originalStart := time.Date(originalPeriod.Year(), originalPeriod.Month(), 1, 0, 0, 0, 0, now.Location())
	originalEnd := originalStart.AddDate(0, 1, -1)
	currentStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	currentEnd := currentStart.AddDate(0, 1, -1)
	date := func(value time.Time) (string, string) {
		return value.Format("2006-01-02"), value.Format("2006-01-02")
	}
	originalFrom, _ := date(originalStart)
	_, originalTo := date(originalEnd)
	currentFrom, _ := date(currentStart)
	_, currentTo := date(currentEnd)

	sales, err := (&ReportService{}).GetSalesStats(tenantID, originalFrom, originalTo)
	if err != nil || len(sales) != 1 || sales[0].OrderCount != 1 || sales[0].TotalAmount != order.TotalAmount || sales[0].RefundedAmount != order.TotalAmount || sales[0].NetAmount != 0 {
		t.Fatalf("original sales report=%+v err=%v", sales, err)
	}
	currentSales, err := (&ReportService{}).GetSalesStats(tenantID, currentFrom, currentTo)
	if err != nil || len(currentSales) != 0 {
		t.Fatalf("refund-action sales report=%+v err=%v", currentSales, err)
	}

	channels, err := (&ReportService{}).GetChannelStats(tenantID, originalFrom, originalTo)
	if err != nil || len(channels) != 1 || channels[0].OrderCount != 1 || channels[0].NetAmount != 0 {
		t.Fatalf("original channel report=%+v err=%v", channels, err)
	}
	currentChannels, err := (&ReportService{}).GetChannelStats(tenantID, currentFrom, currentTo)
	if err != nil || len(currentChannels) != 0 {
		t.Fatalf("refund-action channel report=%+v err=%v", currentChannels, err)
	}

	products, err := (&ReportService{}).GetProductStats(tenantID, originalFrom, originalTo)
	if err != nil || len(products) != 1 || products[0].TotalSold != 0 || products[0].TotalAmount != 0 {
		t.Fatalf("original product report=%+v err=%v", products, err)
	}
	currentProducts, err := (&ReportService{}).GetProductStats(tenantID, currentFrom, currentTo)
	if err != nil || len(currentProducts) != 0 {
		t.Fatalf("refund-action product report=%+v err=%v", currentProducts, err)
	}

	daily, err := (&ReportService{}).GetDailyReport(tenantID, originalFrom, originalTo)
	if err != nil || len(daily.Sales) != 1 || daily.Sales[0].OrderCount != 1 || daily.Sales[0].NetCents != 0 {
		t.Fatalf("original daily sales report=%+v err=%v", daily, err)
	}
	currentDaily, err := (&ReportService{}).GetDailyReport(tenantID, currentFrom, currentTo)
	if err != nil || len(currentDaily.Sales) != 0 {
		t.Fatalf("refund-action daily sales report=%+v err=%v", currentDaily, err)
	}
}
