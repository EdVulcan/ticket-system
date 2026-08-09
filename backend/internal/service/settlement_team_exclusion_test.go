package service

import (
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestOrdinarySettlementExcludesOnlyTicketsBoundToTeamMembers(t *testing.T) {
	resetBusinessData(t)
	distributorID, first, second, bundle := seedCrossSupplierBundle(t, 3)
	periodStart := time.Now().Add(-time.Hour).Truncate(time.Second)
	order := model.Order{
		TenantID: distributorID,
		Channel:  "online",
		Items:    []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 2}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create cross-supplier order: %v", err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, distributorID); err != nil {
		t.Fatalf("pay cross-supplier order: %v", err)
	}

	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	firstTickets := make([]model.Ticket, 0, 2)
	secondTickets := make([]model.Ticket, 0, 2)
	for i := range tickets {
		switch tickets[i].FulfillmentTenantID {
		case first.supplierID:
			firstTickets = append(firstTickets, tickets[i])
		case second.supplierID:
			secondTickets = append(secondTickets, tickets[i])
		}
	}
	if len(firstTickets) != 2 || len(secondTickets) != 2 {
		t.Fatalf("supplier ticket counts=%d/%d, want 2/2", len(firstTickets), len(secondTickets))
	}
	for i := range tickets {
		checkpointID, deviceID := first.checkpointID, first.deviceID
		if tickets[i].FulfillmentTenantID == second.supplierID {
			checkpointID, deviceID = second.checkpointID, second.deviceID
		}
		if err := (&TicketService{}).Verify(tickets[i].TicketCode, checkpointID, deviceID, tickets[i].FulfillmentTenantID); err != nil {
			t.Fatalf("verify supplier %d ticket %s: %v", tickets[i].FulfillmentTenantID, tickets[i].TicketCode, err)
		}
	}

	// AttachOrder permits a paid order to have more tickets than roster members.
	// A cancelled member remains historical team responsibility and must not
	// leak back into ordinary supplier/distributor settlement.
	group := model.TourGroup{
		TenantID:         distributorID,
		SupplierTenantID: first.supplierID,
		ScenicAreaID:     firstTickets[0].FulfillmentScenicAreaID,
		SalesOrderID:     order.ID,
		GroupNo:          fmt.Sprintf("TEAM-SETTLEMENT-%d", time.Now().UnixNano()),
		Name:             "Partial supplier team scope",
		VisitDate:        time.Now(),
		ExpectedCount:    1,
		Status:           "entered",
		SettlementStatus: "open",
	}
	member := model.TourGroupMember{
		Name:       "Historical member",
		TicketCode: firstTickets[0].TicketCode,
		Status:     "cancelled",
	}
	crossTenantGroup := model.TourGroup{
		TenantID:         first.supplierID,
		SupplierTenantID: second.supplierID,
		ScenicAreaID:     secondTickets[0].FulfillmentScenicAreaID,
		SalesOrderID:     order.ID,
		GroupNo:          fmt.Sprintf("CROSS-TENANT-TEAM-%d", time.Now().UnixNano()),
		Name:             "Invalid cross-tenant history",
		VisitDate:        time.Now(),
		ExpectedCount:    1,
		Status:           "cancelled",
		SettlementStatus: "open",
	}
	crossTenantMember := model.TourGroupMember{
		Name:       "Cross-tenant member",
		TicketCode: secondTickets[0].TicketCode,
		Status:     "cancelled",
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		member.GroupID = group.ID
		if err := tx.Create(&member).Error; err != nil {
			return err
		}
		if err := tx.Create(&crossTenantGroup).Error; err != nil {
			return err
		}
		crossTenantMember.GroupID = crossTenantGroup.ID
		return tx.Create(&crossTenantMember).Error
	}); err != nil {
		t.Fatalf("create partial team binding: %v", err)
	}

	periodEnd := time.Now().Add(time.Hour).Truncate(time.Second)
	firstStatement, err := (&SettlementService{}).GenerateStatement(first.supplierID, first.supplierID, distributorID, periodStart, periodEnd)
	if err != nil {
		t.Fatalf("generate first supplier ordinary settlement: %v", err)
	}
	if firstStatement.GrossCents != 6000 || firstStatement.NetCents != 6000 {
		t.Fatalf("first supplier settlement=%+v, want only the unbound 6000-cent ticket", firstStatement)
	}
	secondStatement, err := (&SettlementService{}).GenerateStatement(second.supplierID, second.supplierID, distributorID, periodStart, periodEnd)
	if err != nil {
		t.Fatalf("generate second supplier ordinary settlement: %v", err)
	}
	if secondStatement.GrossCents != 8000 || secondStatement.NetCents != 8000 {
		t.Fatalf("second supplier settlement=%+v, want both 4000-cent tickets", secondStatement)
	}

	var teamCheckIn model.CheckInRecord
	if err := model.DB.Where("ticket_id = ? AND result = ?", firstTickets[0].ID, "success").First(&teamCheckIn).Error; err != nil {
		t.Fatal(err)
	}
	if teamCheckIn.SettlementLineID != 0 {
		t.Fatalf("team-bound ticket was claimed by ordinary settlement line %d", teamCheckIn.SettlementLineID)
	}
	var ordinaryClaimed int64
	if err := model.DB.Model(&model.CheckInRecord{}).
		Where("ticket_id IN ? AND settlement_line_id != 0", []uint{firstTickets[1].ID, secondTickets[0].ID, secondTickets[1].ID}).
		Count(&ordinaryClaimed).Error; err != nil {
		t.Fatal(err)
	}
	if ordinaryClaimed != 3 {
		t.Fatalf("ordinary settlement claimed %d eligible admissions, want 3", ordinaryClaimed)
	}
}
