package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type pooledTicketFixture struct {
	tenantID    uint
	productID   uint
	checkpoints []model.CheckPoint
	devices     []model.Device
}

func seedPooledTicketFixture(t *testing.T, codeMode string, checkpointCount, groupLimit, perCheckpoint int) pooledTicketFixture {
	t.Helper()
	if checkpointCount < 1 {
		t.Fatal("checkpoint count must be positive")
	}
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	fixture := pooledTicketFixture{tenantID: tenantID, productID: productID}
	if err := model.Write(func(tx *gorm.DB) error {
		var product model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return err
		}
		var group model.RuleGroup
		if err := tx.Where("rule_id = ?", product.RuleID).First(&group).Error; err != nil {
			return err
		}
		var first model.CheckPoint
		if err := tx.Where("tenant_id = ? AND scenic_area_id = ?", tenantID, product.ScenicAreaID).Order("id ASC").First(&first).Error; err != nil {
			return err
		}
		fixture.checkpoints = append(fixture.checkpoints, first)
		if err := tx.Model(&model.RuleGroup{}).Where("id = ?", group.ID).Update("max_total_check_in", groupLimit).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.RuleItem{}).Where("group_id = ? AND check_point_id = ?", group.ID, first.ID).Update("max_per_check_in", perCheckpoint).Error; err != nil {
			return err
		}
		for index := 1; index < checkpointCount; index++ {
			checkpoint := model.CheckPoint{
				Name:         fmt.Sprintf("Pooled Gate %d", index+1),
				TenantID:     tenantID,
				ScenicAreaID: product.ScenicAreaID,
			}
			if err := tx.Create(&checkpoint).Error; err != nil {
				return err
			}
			fixture.checkpoints = append(fixture.checkpoints, checkpoint)
			checkpointID := checkpoint.ID
			device := model.Device{
				Name:              fmt.Sprintf("Pooled Device %d", index+1),
				SerialNumber:      fmt.Sprintf("POOLED-%d-%d", tenantID, index+1),
				Type:              "gate",
				Status:            "online",
				TenantID:          tenantID,
				ScenicAreaID:      product.ScenicAreaID,
				CheckPointID:      &checkpointID,
				AuthKeyCiphertext: encryptedDeviceKeyForTest(t, "test-device-key"),
			}
			if err := tx.Create(&device).Error; err != nil {
				return err
			}
			fixture.devices = append(fixture.devices, device)
			if err := tx.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: checkpoint.ID, MaxPerCheckIn: perCheckpoint}).Error; err != nil {
				return err
			}
		}
		var firstDevice model.Device
		if err := tx.Where("tenant_id = ? AND check_point_id = ?", tenantID, first.ID).First(&firstDevice).Error; err != nil {
			return err
		}
		fixture.devices = append([]model.Device{firstDevice}, fixture.devices...)
		return tx.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", productID, tenantID).Update("code_mode", codeMode).Error
	}); err != nil {
		t.Fatalf("seed pooled ticket fixture: %v", err)
	}
	return fixture
}

func createPaidPooledOrder(t *testing.T, fixture pooledTicketFixture, quantity int) model.Ticket {
	t.Helper()
	order := model.Order{
		TenantID: fixture.tenantID,
		Channel:  "window",
		Items:    []model.OrderItem{{ProductID: fixture.productID, Quantity: quantity}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create pooled order: %v", err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatalf("pay pooled order: %v", err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatalf("load pooled ticket: %v", err)
	}
	var rule model.TicketRule
	if err := json.Unmarshal([]byte(ticket.RuleSnapshot), &rule); err != nil {
		t.Fatalf("decode pooled rule snapshot: %v", err)
	}
	if rule.AdmissionPolicy != ticketAdmissionPolicyPooledV1 {
		t.Fatalf("admission policy=%q, want %q", rule.AdmissionPolicy, ticketAdmissionPolicyPooledV1)
	}
	return ticket
}

func verifyPooledTicket(t *testing.T, fixture pooledTicketFixture, ticket model.Ticket, checkpointIndex int) error {
	t.Helper()
	if checkpointIndex < 0 || checkpointIndex >= len(fixture.checkpoints) {
		t.Fatalf("checkpoint index %d out of range", checkpointIndex)
	}
	return (&TicketService{}).Verify(ticket.TicketCode, fixture.checkpoints[checkpointIndex].ID, fixture.devices[checkpointIndex].ID, fixture.tenantID)
}

func TestPooledGroupProjectionUsesWeightedSelections(t *testing.T) {
	group := model.RuleGroup{
		MaxTotalCheckIn: 2,
		Items: []model.RuleItem{
			{CheckPointID: 1, MaxPerCheckIn: 3},
			{CheckPointID: 2, MaxPerCheckIn: 1},
		},
	}
	product := model.Product{CodeMode: "ticket", Rule: model.TicketRule{AdmissionPolicy: ticketAdmissionPolicyPooledV1}}
	item := model.OrderItem{Quantity: 1}
	records := []model.CheckInRecord{
		{CheckPointID: 1},
		{CheckPointID: 1},
		{CheckPointID: 1},
	}
	if !groupAllowsCheckpointForTicket(records, &product, &item, &group, 1) {
		t.Fatal("a fourth admission at A should consume the second weighted selection")
	}
	if !groupAllowsCheckpointForTicket(records, &product, &item, &group, 2) {
		t.Fatal("A x3 plus B should fit two weighted selections")
	}
	records = append(records, model.CheckInRecord{CheckPointID: 2})
	if groupAllowsCheckpointForTicket(records, &product, &item, &group, 2) {
		t.Fatal("a second B admission should exceed the weighted group budget")
	}

	pooledProduct := model.Product{CodeMode: "order", Rule: model.TicketRule{AdmissionPolicy: ticketAdmissionPolicyPooledV1}}
	pooledItem := model.OrderItem{Quantity: 2}
	if !groupAllowsCheckpointForTicket([]model.CheckInRecord{{CheckPointID: 1}}, &pooledProduct, &pooledItem, &model.RuleGroup{
		MaxTotalCheckIn: 1,
		Items:           []model.RuleItem{{CheckPointID: 1, MaxPerCheckIn: 1}, {CheckPointID: 2, MaxPerCheckIn: 1}},
	}, 2) {
		t.Fatal("two pooled tickets should allow A then B in a one-select group")
	}
}

func TestPooledOrderQRCodeScalesPointAndGroupLimits(t *testing.T) {
	resetBusinessData(t)
	fixture := seedPooledTicketFixture(t, "order", 4, 3, 1)
	ticket := createPaidPooledOrder(t, fixture, 6)

	for index := 0; index < 6; index++ {
		if err := verifyPooledTicket(t, fixture, ticket, 0); err != nil {
			t.Fatalf("A admission %d: %v", index+1, err)
		}
	}
	if err := verifyPooledTicket(t, fixture, ticket, 0); !errors.Is(err, ErrPointLimitReached) {
		t.Fatalf("seventh A admission error=%v, want point limit", err)
	}
	for index := 0; index < 6; index++ {
		if err := verifyPooledTicket(t, fixture, ticket, 1); err != nil {
			t.Fatalf("B admission %d: %v", index+1, err)
		}
	}
	for index := 0; index < 6; index++ {
		if err := verifyPooledTicket(t, fixture, ticket, 2); err != nil {
			t.Fatalf("C admission %d: %v", index+1, err)
		}
	}
	var stored model.Ticket
	if err := model.DB.First(&stored, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "used" || stored.CheckInCount != 18 {
		t.Fatalf("pooled ticket status=%s count=%d, want used/18", stored.Status, stored.CheckInCount)
	}
	var successful int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND result = ?", ticket.ID, "success").Count(&successful).Error; err != nil {
		t.Fatal(err)
	}
	if successful != 18 {
		t.Fatalf("successful pooled admissions=%d, want 18", successful)
	}
}

func TestPooledOrderCodeAllowsTwoPointsForTwoTickets(t *testing.T) {
	resetBusinessData(t)
	fixture := seedPooledTicketFixture(t, "order", 2, 1, 1)
	ticket := createPaidPooledOrder(t, fixture, 2)
	if err := verifyPooledTicket(t, fixture, ticket, 0); err != nil {
		t.Fatalf("A admission: %v", err)
	}
	if err := verifyPooledTicket(t, fixture, ticket, 1); err != nil {
		t.Fatalf("B admission: %v", err)
	}
	var stored model.Ticket
	if err := model.DB.First(&stored, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "used" || stored.CheckInCount != 2 {
		t.Fatalf("two-ticket pooled status=%s count=%d, want used/2", stored.Status, stored.CheckInCount)
	}
}

func TestTicketCodeKeepsQuantityAtOneAdmissionUnit(t *testing.T) {
	resetBusinessData(t)
	fixture := seedPooledTicketFixture(t, "ticket", 1, 1, 1)
	order := model.Order{TenantID: fixture.tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 {
		t.Fatalf("ticket-code quantity produced %d tickets, want 2", len(tickets))
	}
	for index := range tickets {
		if err := verifyPooledTicket(t, fixture, tickets[index], 0); err != nil {
			t.Fatalf("ticket %d admission: %v", index+1, err)
		}
	}
	if err := verifyPooledTicket(t, fixture, tickets[0], 0); !errors.Is(err, ErrTicketUnavailable) {
		t.Fatalf("repeat ticket-code admission error=%v, want unavailable", err)
	}
}

func TestPooledGroupsRemainIndependent(t *testing.T) {
	groupA := model.RuleGroup{MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: 1, MaxPerCheckIn: 1}, {CheckPointID: 2, MaxPerCheckIn: 1}}}
	groupB := model.RuleGroup{MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: 3, MaxPerCheckIn: 1}, {CheckPointID: 4, MaxPerCheckIn: 1}}}
	records := []model.CheckInRecord{{CheckPointID: 3}}
	product := model.Product{CodeMode: "ticket", Rule: model.TicketRule{AdmissionPolicy: ticketAdmissionPolicyPooledV1}}
	item := model.OrderItem{Quantity: 1}
	if !groupAllowsCheckpointForTicket(records, &product, &item, &groupA, 1) {
		t.Fatal("group A should ignore usage belonging to group B")
	}
	if groupAllowsCheckpointForTicket(records, &product, &item, &groupB, 4) {
		t.Fatal("group B should enforce its own selection budget")
	}
}

func TestUnmarkedRuleKeepsDistinctCheckpointSemantics(t *testing.T) {
	group := model.RuleGroup{
		MaxTotalCheckIn: 1,
		Items: []model.RuleItem{
			{CheckPointID: 1, MaxPerCheckIn: 3},
			{CheckPointID: 2, MaxPerCheckIn: 3},
		},
	}
	records := []model.CheckInRecord{{CheckPointID: 1}, {CheckPointID: 1}, {CheckPointID: 1}}
	product := model.Product{CodeMode: "ticket", Rule: model.TicketRule{Groups: []model.RuleGroup{group}}}
	item := model.OrderItem{Quantity: 1}
	if !groupAllowsCheckpoint(records, &group, 1) {
		t.Fatal("unmarked rule should continue allowing an already-used checkpoint")
	}
	if !groupAllowsCheckpointForTicket(records, &product, &item, &group, 1) {
		t.Fatal("unmarked ticket should use distinct-checkpoint semantics")
	}
	if hasRemainingAdmission(&product, &item, records) {
		t.Fatal("unmarked ticket should have no remaining admission after its only point reaches its point limit")
	}
}

func TestUnmarkedOrderSnapshotKeepsDistinctGroupSemantics(t *testing.T) {
	resetBusinessData(t)
	fixture := seedPooledTicketFixture(t, "order", 2, 1, 1)
	ticket := createPaidPooledOrder(t, fixture, 2)
	var rule model.TicketRule
	if err := json.Unmarshal([]byte(ticket.RuleSnapshot), &rule); err != nil {
		t.Fatal(err)
	}
	rule.AdmissionPolicy = ""
	truncated, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", ticket.ID).Update("rule_snapshot", string(truncated)).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyPooledTicket(t, fixture, ticket, 0); err != nil {
		t.Fatalf("legacy A admission: %v", err)
	}
	if err := verifyPooledTicket(t, fixture, ticket, 1); !errors.Is(err, ErrGroupLimitReached) {
		t.Fatalf("legacy B admission error=%v, want group limit", err)
	}
	var stored model.Ticket
	if err := model.DB.First(&stored, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "active" || stored.CheckInCount != 1 {
		t.Fatalf("legacy ticket status=%s count=%d, want active/1", stored.Status, stored.CheckInCount)
	}
}
