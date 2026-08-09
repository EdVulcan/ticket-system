package service

import (
	"sort"
	"sync"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func createConfirmedTeamForConfirmationTest(t *testing.T, memberCount int) (teamP0Fixture, model.TourGroup) {
	t.Helper()
	fixture := seedTeamP0Fixture(t, 1000000)
	group := createTeamP0Group(t, fixture, "Confirmation Guard Team", memberCount)
	if _, err := (&TeamService{}).CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err != nil {
		t.Fatalf("create confirmed team order: %v", err)
	}
	return fixture, group
}

func TestSubmitTeamConfirmationRequiresActiveTravelCapability(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 1)
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.travel.ID, "travel_agency").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&TeamService{}).SubmitTeamConfirmation(fixture.travel.ID, group.ID, fixture.operator.ID, 1, 0, 0, ""); err == nil {
		t.Fatal("suspended travel capability submitted a team confirmation")
	}
	var count int64
	if err := model.DB.Model(&model.TourGroupConfirmation{}).Where("group_id = ?", group.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("confirmation rows=%d, want 0", count)
	}
}

func TestTeamConfirmationCountDifferenceRequiresNotes(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 2)
	service := &TeamService{}
	if _, err := service.SubmitTeamConfirmation(fixture.travel.ID, group.ID, fixture.operator.ID, 1, 0, 0, ""); err == nil {
		t.Fatal("confirmation count differed from roster without notes")
	}
	if _, err := service.SubmitTeamConfirmation(fixture.travel.ID, group.ID, fixture.operator.ID, 1, 0, 0, "one visitor did not arrive"); err != nil {
		t.Fatalf("documented confirmation difference was rejected: %v", err)
	}
	if _, err := service.SubmitTeamConfirmation(fixture.travel.ID, group.ID, fixture.operator.ID, 2, 0, 0, ""); err != nil {
		t.Fatalf("matching confirmation count required unnecessary notes: %v", err)
	}
}

func TestChangeTeamMemberRequiresActiveTravelCapability(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 1)
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.travel.ID, "travel_agency").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&TeamService{}).ChangeTeamMember(fixture.travel.ID, group.ID, fixture.operator.ID, "add", 0, model.TourGroupMember{Name: "Temporary Visitor"}, "onsite addition"); err == nil {
		t.Fatal("suspended travel capability changed the team roster")
	}
	var count int64
	if err := model.DB.Model(&model.TourGroupMemberChange{}).Where("group_id = ?", group.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("member change rows=%d, want 0", count)
	}
}

func TestChangeTeamMemberWithoutSpareTicketDoesNotCreateMember(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 1)
	var before int64
	if err := model.DB.Model(&model.TourGroupMember{}).Where("group_id = ?", group.ID).Count(&before).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&TeamService{}).ChangeTeamMember(fixture.travel.ID, group.ID, fixture.operator.ID, "add", 0, model.TourGroupMember{Name: "Visitor Without Ticket"}, "onsite addition"); err == nil {
		t.Fatal("member without a spare ticket was persisted")
	}
	var after int64
	if err := model.DB.Model(&model.TourGroupMember{}).Where("group_id = ?", group.ID).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("team members changed from %d to %d after rejected addition", before, after)
	}
}

func TestChangeTeamMemberAcceptsVoidedTicket(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 1)
	var member model.TourGroupMember
	if err := model.DB.Where("group_id = ?", group.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if member.TicketCode == "" {
		t.Fatal("confirmed member has no ticket")
	}
	if err := model.DB.Model(&model.Ticket{}).Where("ticket_code = ?", member.TicketCode).Update("status", "void").Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&TeamService{}).ChangeTeamMember(fixture.travel.ID, group.ID, fixture.operator.ID, "remove", member.ID, model.TourGroupMember{}, "visitor cancelled"); err != nil {
		t.Fatalf("voided team ticket could not be removed: %v", err)
	}
	if err := model.DB.First(&member, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if member.Status != "cancelled" {
		t.Fatalf("member status=%s, want cancelled", member.Status)
	}
}

func TestConcurrentTeamConfirmationAndMemberChangeSequencesRemainStable(t *testing.T) {
	fixture, group := createConfirmedTeamForConfirmationTest(t, 2)
	service := &TeamService{}

	confirmationErrors := make(chan error, 2)
	var confirmationWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		confirmationWG.Add(1)
		go func() {
			defer confirmationWG.Done()
			_, err := service.SubmitTeamConfirmation(fixture.travel.ID, group.ID, fixture.operator.ID, 2, 0, 0, "")
			confirmationErrors <- err
		}()
	}
	confirmationWG.Wait()
	close(confirmationErrors)
	for err := range confirmationErrors {
		if err != nil {
			t.Fatalf("concurrent confirmation: %v", err)
		}
	}
	var confirmations []model.TourGroupConfirmation
	if err := model.DB.Where("group_id = ?", group.ID).Order("sequence").Find(&confirmations).Error; err != nil {
		t.Fatal(err)
	}
	if len(confirmations) != 2 || confirmations[0].Sequence != 1 || confirmations[1].Sequence != 2 {
		t.Fatalf("confirmation sequences=%+v", confirmations)
	}

	var members []model.TourGroupMember
	if err := model.DB.Where("group_id = ?", group.ID).Order("id").Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members=%d, want 2", len(members))
	}
	if err := model.Write(func(tx *gorm.DB) error {
		codes := []string{members[0].TicketCode, members[1].TicketCode}
		return tx.Model(&model.Ticket{}).Where("ticket_code IN ?", codes).Update("status", "void").Error
	}); err != nil {
		t.Fatal(err)
	}

	changeErrors := make(chan error, 2)
	var changeWG sync.WaitGroup
	for _, member := range members {
		memberID := member.ID
		changeWG.Add(1)
		go func() {
			defer changeWG.Done()
			_, err := service.ChangeTeamMember(fixture.travel.ID, group.ID, fixture.operator.ID, "remove", memberID, model.TourGroupMember{}, "visitor cancelled")
			changeErrors <- err
		}()
	}
	changeWG.Wait()
	close(changeErrors)
	for err := range changeErrors {
		if err != nil {
			t.Fatalf("concurrent member change: %v", err)
		}
	}
	var changes []model.TourGroupMemberChange
	if err := model.DB.Where("group_id = ?", group.ID).Find(&changes).Error; err != nil {
		t.Fatal(err)
	}
	sequences := make([]int, 0, len(changes))
	for _, change := range changes {
		sequences = append(sequences, change.Sequence)
	}
	sort.Ints(sequences)
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("member change sequences=%v", sequences)
	}
}
