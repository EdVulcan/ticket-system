package service

import (
	"testing"
	"time"

	"ticket-backend/internal/model"
)

func createRefundProjectionTeamOrder(t *testing.T, scenario distributionScenario, used bool) (model.Order, model.Ticket, model.FulfillmentOrder, model.TourGroup, model.TourGroupMember) {
	t.Helper()
	order := model.Order{
		TenantID: scenario.distributorID,
		Channel:  "online",
		Items:    []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID: scenario.distributorID, PaymentNo: "TEAM-REFUND-PAY-" + order.OrderNo,
		OrderNo: order.OrderNo, Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount),
		Method: "cash", Status: "paid",
	}
	if err := model.DB.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	var fulfillment model.FulfillmentOrder
	if err := model.DB.Where("sales_order_id = ? AND supplier_tenant_id = ?", order.ID, scenario.supplierID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	memberStatus, groupStatus := "ticketed", "confirmed"
	var enteredAt *time.Time
	if used {
		if err := (&TicketService{}).Verify(ticket.TicketCode, scenario.supplierCheckpointID, scenario.supplierDeviceID, scenario.supplierID); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		enteredAt, memberStatus, groupStatus = &now, "entered", "entered"
	}
	group := model.TourGroup{
		TenantID: scenario.distributorID, SupplierTenantID: scenario.supplierID,
		ScenicAreaID: fulfillment.ScenicAreaID, SalesOrderID: order.ID,
		GroupNo: "TEAM-REFUND-" + order.OrderNo, Name: "退款投影测试团",
		VisitDate: time.Now(), ExpectedCount: 1, Status: groupStatus,
		ContractAmountCents: moneyCents(order.TotalAmount), CreditUsedCents: moneyCents(order.TotalAmount),
		SettlementStatus: "open",
	}
	if err := model.DB.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	member := model.TourGroupMember{
		GroupID: group.ID, Name: "测试游客", TicketCode: ticket.TicketCode,
		Status: memberStatus, EnteredAt: enteredAt,
	}
	if used {
		member.EntryBatchNo = "BATCH-BEFORE-REFUND"
	}
	if err := model.DB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	return order, ticket, fulfillment, group, member
}

func assertRefundedTeamProjection(t *testing.T, groupID, memberID uint) {
	t.Helper()
	var member model.TourGroupMember
	if err := model.DB.First(&member, memberID).Error; err != nil {
		t.Fatal(err)
	}
	if member.Status != "cancelled" || member.EnteredAt != nil || member.EntryBatchNo != "" {
		t.Fatalf("team member after refund=%+v", member)
	}
	var group model.TourGroup
	if err := model.DB.First(&group, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if group.ExpectedCount != 0 || group.Status != "cancelled" {
		t.Fatalf("team group after refund=%+v", group)
	}
	var changes []model.TourGroupMemberChange
	if err := model.DB.Where("group_id = ? AND member_id = ? AND change_type = ?", groupID, memberID, "remove").Find(&changes).Error; err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].BeforeExpectedCount != 1 || changes[0].AfterExpectedCount != 0 || changes[0].Reason == "" {
		t.Fatalf("refund member changes=%+v", changes)
	}
}

func TestUnusedTeamTicketRefundCancelsMemberAndRecalculatesGroup(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order, ticket, _, group, member := createRefundProjectionTeamOrder(t, scenario, false)

	if _, err := (&RefundService{}).CreateCashRefund(
		scenario.distributorID, order.OrderNo, "team-unused-projection", order.TotalAmount,
		[]string{ticket.TicketCode}, "未核销团队票退款",
	); err != nil {
		t.Fatal(err)
	}
	assertRefundedTeamProjection(t, group.ID, member.ID)
}

func TestUsedTeamTicketRefundCancelsMemberAndReversesAdmission(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	_, ticket, fulfillment, group, member := createRefundProjectionTeamOrder(t, scenario, true)
	initial := model.User{
		TenantID: scenario.supplierID, Username: "refund-projection-initial", Password: "test",
		Role: "super_admin", IsInitialAdmin: true,
	}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&RefundService{}).CreateSupplierUsedRefund(
		RefundActor{TenantID: scenario.supplierID, UserID: initial.ID}, fulfillment.ID,
		"team-used-projection", []string{ticket.TicketCode}, "已核销团队票退款",
	); err != nil {
		t.Fatal(err)
	}
	assertRefundedTeamProjection(t, group.ID, member.ID)

	var activeAdmissions int64
	if err := model.DB.Model(&model.CheckInRecord{}).
		Where("ticket_id = ? AND result = ? AND reversed_at IS NULL", ticket.ID, "success").
		Count(&activeAdmissions).Error; err != nil {
		t.Fatal(err)
	}
	if activeAdmissions != 0 {
		t.Fatalf("active admissions after used-ticket refund=%d", activeAdmissions)
	}

	// The business refund is idempotent; replaying it must not alter the projection.
	if _, err := (&RefundService{}).CreateSupplierUsedRefund(
		RefundActor{TenantID: scenario.supplierID, UserID: initial.ID}, fulfillment.ID,
		"team-used-projection", []string{ticket.TicketCode}, "已核销团队票退款",
	); err != nil {
		t.Fatal(err)
	}
	assertRefundedTeamProjection(t, group.ID, member.ID)
}
