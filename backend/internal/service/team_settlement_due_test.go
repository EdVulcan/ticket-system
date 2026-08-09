package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestTeamSettlementUsesContractDueDateAndBusinessNames(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	if err := model.DB.Model(&model.TravelContract{}).Where("id = ?", fixture.contract.ID).Update("settlement_days", 15).Error; err != nil {
		t.Fatal(err)
	}
	group := createTeamP0Group(t, fixture, "账期测试团队", 1)
	team := &TeamService{}
	if _, err := team.CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err != nil {
		t.Fatal(err)
	}
	members, err := team.ListMembers(fixture.travel.ID, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := team.EnterBatch(fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID, []uint{members[0].ID}, "settlement-due-entry"); err != nil {
		t.Fatal(err)
	}

	generatedAt := time.Now()
	statement, err := team.GenerateTeamSettlement(fixture.travel.ID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if statement.DueAt == nil {
		t.Fatal("team settlement did not snapshot the contract due date")
	}
	wantDueAt := generatedAt.AddDate(0, 0, 15)
	if statement.DueAt.Before(wantDueAt.Add(-5*time.Second)) || statement.DueAt.After(wantDueAt.Add(5*time.Second)) {
		t.Fatalf("due_at=%v want around %v", statement.DueAt, wantDueAt)
	}

	rows, total, err := team.ListTeamSettlements(fixture.travel.ID, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("settlements=%+v total=%d err=%v", rows, total, err)
	}
	if rows[0].TravelTenantName != fixture.travel.Name || rows[0].SupplierTenantName != fixture.supplier.Name || rows[0].GroupNo != group.GroupNo || rows[0].GroupName != group.Name {
		t.Fatalf("settlement business names=%+v", rows[0])
	}
	accounts, err := team.ListTeamAccountSummaries(fixture.travel.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	if accounts[0].TravelTenantName != fixture.travel.Name || accounts[0].SupplierTenantName != fixture.supplier.Name {
		t.Fatalf("account business names=%+v", accounts[0])
	}
}
