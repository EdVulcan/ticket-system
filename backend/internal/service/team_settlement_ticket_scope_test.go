package service

import (
	"fmt"
	"testing"
	"time"

	"ticket-backend/internal/model"
)

func TestTeamAttachOrderFundingOnlyCoversAssignedMemberTickets(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Partially Attached Funding Team", 1)

	if err := (&TeamService{}).AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	const assignedTicketSettlementCents = int64(6000)
	if stored.ContractAmountCents != assignedTicketSettlementCents || stored.DepositCents != assignedTicketSettlementCents || stored.CreditUsedCents != 0 {
		t.Fatalf("team funding contract=%d deposit=%d credit=%d, want assigned ticket amount %d with no credit", stored.ContractAmountCents, stored.DepositCents, stored.CreditUsedCents, assignedTicketSettlementCents)
	}
}

func TestGenerateTeamSettlementOnlyUsesStrictlyScopedMemberTickets(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Strict Ticket Scope Team", 1)
	team := &TeamService{}
	if err := team.AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	members, err := team.ListMembers(fixture.travel.ID, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := team.EnterBatch(fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID, []uint{members[0].ID}, "strict-scope-entry"); err != nil {
		t.Fatal(err)
	}

	var orderTickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&orderTickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(orderTickets) != 2 || orderTickets[0].TicketCode != members[0].TicketCode {
		t.Fatalf("order tickets=%+v member=%+v", orderTickets, members[0])
	}
	if fixture.device.CheckPointID == nil {
		t.Fatal("team device has no checkpoint")
	}
	if err := (&TicketService{}).Verify(orderTickets[1].TicketCode, *fixture.device.CheckPointID, fixture.device.ID, fixture.supplier.ID); err != nil {
		t.Fatal(err)
	}

	// The unbound ticket belongs to a different, already-cancelled group. It is
	// a historical team fact, but must never be included in this group's bill.
	otherGroup := model.TourGroup{
		TenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID,
		ContractID: fixture.contract.ID, SalesOrderID: order.ID,
		GroupNo: fmt.Sprintf("OTHER-CANCELLED-%d", time.Now().UnixNano()), Name: "Other Cancelled Team",
		VisitDate: time.Now().Add(24 * time.Hour), ExpectedCount: 0, Status: "cancelled", SettlementStatus: "open",
	}
	if err := model.DB.Create(&otherGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TourGroupMember{
		GroupID: otherGroup.ID, Name: "Other Team Visitor", TicketCode: orderTickets[1].TicketCode, Status: "cancelled",
	}).Error; err != nil {
		t.Fatal(err)
	}

	// A cancelled member keeps its historical ticket link so a later refund can
	// reverse the admission amount without presenting the member as admitted.
	now := time.Now()
	if err := model.DB.Model(&model.CheckInRecord{}).
		Where("ticket_id = ? AND result = ?", orderTickets[0].ID, "success").
		Update("reversed_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.TourGroupMember{}).Where("id = ?", members[0].ID).
		Updates(map[string]interface{}{"status": "cancelled", "entered_at": nil, "entry_batch_no": ""}).Error; err != nil {
		t.Fatal(err)
	}

	foreign := seedDistributionScenario(t)
	foreignOrder := model.Order{TenantID: foreign.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: foreign.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&foreignOrder); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(foreignOrder.OrderNo, foreign.distributorID); err != nil {
		t.Fatal(err)
	}
	var foreignTicket model.Ticket
	if err := model.DB.Where("order_id = ?", foreignOrder.ID).First(&foreignTicket).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(foreignTicket.TicketCode, foreign.supplierCheckpointID, foreign.supplierDeviceID, foreign.supplierID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TourGroupMember{
		GroupID: group.ID, Name: "Forged Foreign Visitor", TicketCode: foreignTicket.TicketCode, Status: "cancelled",
	}).Error; err != nil {
		t.Fatal(err)
	}

	statement, err := team.GenerateTeamSettlement(fixture.travel.ID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if statement.GrossCents != 6000 || statement.RefundCents != 6000 || statement.DepositCents != 0 || statement.NetCents != 0 {
		t.Fatalf("strictly scoped statement=%+v", statement)
	}
}

func TestTeamSettlementRefundRefreshIgnoresUnboundTicketAndReleasesBoundFunding(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Refund Scope Team", 1)
	team := &TeamService{}
	if err := team.AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	members, err := team.ListMembers(fixture.travel.ID, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := team.EnterBatch(fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID, []uint{members[0].ID}, "refund-scope-entry"); err != nil {
		t.Fatal(err)
	}

	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].TicketCode != members[0].TicketCode {
		t.Fatalf("tickets=%+v member=%+v", tickets, members[0])
	}
	if fixture.device.CheckPointID == nil {
		t.Fatal("team device has no checkpoint")
	}
	if err := (&TicketService{}).Verify(tickets[1].TicketCode, *fixture.device.CheckPointID, fixture.device.ID, fixture.supplier.ID); err != nil {
		t.Fatal(err)
	}

	// Seed the correct pre-refund projection so this test isolates the refresh
	// path from the separate generation regression above.
	if err := model.DB.Model(&model.TourGroup{}).Where("id = ?", group.ID).Updates(map[string]interface{}{
		"contract_amount_cents": 6000, "deposit_cents": 6000, "credit_used_cents": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	statement := model.TeamSettlementStatement{
		TravelTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID, GroupID: group.ID,
		Sequence: 1, Kind: "original", StatementNo: fmt.Sprintf("TST-SCOPE-%d", time.Now().UnixNano()),
		IdempotencyKey: fmt.Sprintf("team-settlement:%d", group.ID), GrossCents: 6000,
		RefundCents: 0, DepositCents: 6000, NetCents: 0, Status: "draft",
	}
	if err := model.DB.Create(&statement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Payment{
		TenantID: fixture.travel.ID, PaymentNo: fmt.Sprintf("SCOPE-PAY-%d", time.Now().UnixNano()),
		OrderNo: order.OrderNo, Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount),
		Method: "cash", Status: "paid",
	}).Error; err != nil {
		t.Fatal(err)
	}
	initial := model.User{
		TenantID: fixture.supplier.ID, Username: fmt.Sprintf("scope-initial-%d", time.Now().UnixNano()),
		Password: "test", Role: "super_admin", IsInitialAdmin: true,
	}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	var fulfillment model.FulfillmentOrder
	if err := model.DB.Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", order.ID, fixture.supplier.ID, fixture.area.ID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}

	refunds := &RefundService{}
	if _, err := refunds.CreateSupplierUsedRefund(
		RefundActor{TenantID: fixture.supplier.ID, UserID: initial.ID}, fulfillment.ID,
		"unbound-team-ticket-refund", []string{tickets[1].TicketCode}, "refund unbound ticket",
	); err != nil {
		t.Fatal(err)
	}
	var afterUnbound model.TeamSettlementStatement
	if err := model.DB.First(&afterUnbound, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterUnbound.GrossCents != 6000 || afterUnbound.RefundCents != 0 || afterUnbound.DepositCents != 6000 || afterUnbound.NetCents != 0 {
		t.Fatalf("unbound refund changed team statement: %+v", afterUnbound)
	}
	var afterUnboundGroup model.TourGroup
	if err := model.DB.First(&afterUnboundGroup, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterUnboundGroup.DepositCents != 6000 || afterUnboundGroup.CreditUsedCents != 0 {
		t.Fatalf("unbound refund changed team funding: %+v", afterUnboundGroup)
	}

	if _, err := refunds.CreateSupplierUsedRefund(
		RefundActor{TenantID: fixture.supplier.ID, UserID: initial.ID}, fulfillment.ID,
		"bound-team-ticket-refund", []string{tickets[0].TicketCode}, "refund bound ticket",
	); err != nil {
		t.Fatal(err)
	}
	var afterBound model.TeamSettlementStatement
	if err := model.DB.First(&afterBound, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterBound.GrossCents != 6000 || afterBound.RefundCents != 6000 || afterBound.DepositCents != 0 || afterBound.NetCents != 0 || afterBound.Status != "draft" {
		t.Fatalf("bound refund statement=%+v", afterBound)
	}
	var afterBoundGroup model.TourGroup
	if err := model.DB.First(&afterBoundGroup, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterBoundGroup.DepositCents != 0 || afterBoundGroup.CreditUsedCents != 0 {
		t.Fatalf("bound refund did not release team funding: %+v", afterBoundGroup)
	}
}
