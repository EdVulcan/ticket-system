package service

import (
	"testing"
	"ticket-backend/internal/model"
)

func createEnteredTeamSettlementFixture(t *testing.T) (teamP0Fixture, model.TourGroup, *model.TeamSettlementStatement) {
	t.Helper()
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "Settlement Capability Team", 1)
	team := &TeamService{}
	if _, err := team.CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err != nil {
		t.Fatal(err)
	}
	members, err := team.ListMembers(fixture.travel.ID, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := team.EnterBatch(fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID, []uint{members[0].ID}, "settlement-capability-entry"); err != nil {
		t.Fatal(err)
	}
	statement, err := team.GenerateTeamSettlement(fixture.travel.ID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, group, statement
}

func TestSuspendedTravelCapabilityCannotGenerateTeamSettlement(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "Suspended Settlement Team", 1)
	team := &TeamService{}
	if _, err := team.CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err != nil {
		t.Fatal(err)
	}
	members, err := team.ListMembers(fixture.travel.ID, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := team.EnterBatch(fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID, []uint{members[0].ID}, "suspended-settlement-entry"); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.travel.ID, "travel_agency").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := team.GenerateTeamSettlement(fixture.travel.ID, group.ID); err == nil {
		t.Fatal("suspended travel capability generated a team settlement")
	}
}

func TestSuspendedSettlementPartyCannotAdvanceTeamSettlement(t *testing.T) {
	fixture, _, statement := createEnteredTeamSettlementFixture(t)
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.supplier.ID, "supplier").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TeamService{}).SetTeamSettlementStatus(fixture.supplier.ID, statement.ID, "supplier_confirmed", ""); err == nil {
		t.Fatal("suspended supplier capability confirmed a team settlement")
	}
}
