package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func createTeamQueryGroup(t *testing.T, fixture teamP0Fixture, name string, visitDate time.Time, status string) model.TourGroup {
	t.Helper()
	group := model.TourGroup{
		Name: name, SupplierTenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID,
		ContractID: fixture.contract.ID, VisitDate: visitDate, ExpectedCount: 1,
	}
	if err := (&TeamService{}).CreateGroup(fixture.travel.ID, &group); err != nil {
		t.Fatalf("create query group %q: %v", name, err)
	}
	if status != "draft" {
		if err := model.DB.Model(&group).Update("status", status).Error; err != nil {
			t.Fatalf("set query group %q status: %v", name, err)
		}
		group.Status = status
	}
	return group
}

func assertTeamGroupIDs(t *testing.T, groups []model.TourGroup, expected ...uint) {
	t.Helper()
	if len(groups) != len(expected) {
		t.Fatalf("group count=%d want=%d groups=%+v", len(groups), len(expected), groups)
	}
	for i := range expected {
		if groups[i].ID != expected[i] {
			t.Fatalf("group[%d]=%d want=%d order=%+v", i, groups[i].ID, expected[i], groups)
		}
	}
}

func TestListTeamGroupsSupportsOperationalFiltersAndUsefulDateOrder(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	var today time.Time
	if err := model.DB.Raw("SELECT CURRENT_DATE").Scan(&today).Error; err != nil {
		t.Fatal(err)
	}

	current := createTeamQueryGroup(t, fixture, "Current Team", today, "confirmed")
	futureOne := createTeamQueryGroup(t, fixture, "Spring Tour Express", today.AddDate(0, 0, 1), "draft")
	futureTwo := createTeamQueryGroup(t, fixture, "Rate 100% Team", today.AddDate(0, 0, 2).Add(15*time.Hour), "confirmed")
	pastOne := createTeamQueryGroup(t, fixture, "Recent History", today.AddDate(0, 0, -1), "entered")
	pastTwo := createTeamQueryGroup(t, fixture, "Old History", today.AddDate(0, 0, -2), "cancelled")

	order := model.Order{OrderNo: "TEAM-ORDER-SEARCH-7788", TenantID: fixture.travel.ID, Status: "paid", Channel: "team_account"}
	if err := model.DB.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&futureOne).Update("sales_order_id", order.ID).Error; err != nil {
		t.Fatal(err)
	}

	service := &TeamService{}
	groups, total, err := service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total=%d want=5", total)
	}
	assertTeamGroupIDs(t, groups, current.ID, futureOne.ID, futureTwo.ID, pastOne.ID, pastTwo.ID)

	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 1, PageSize: 10, Keyword: "Spring Tour"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("name keyword total=%d want=1", total)
	}
	assertTeamGroupIDs(t, groups, futureOne.ID)

	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 1, PageSize: 10, Keyword: "7788"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || groups[0].SalesOrderNo != order.OrderNo {
		t.Fatalf("order keyword result total=%d groups=%+v", total, groups)
	}
	assertTeamGroupIDs(t, groups, futureOne.ID)
	supplierGroups, supplierTotal, err := service.ListGroupsWithOptions(fixture.supplier.ID, TeamGroupListOptions{Page: 1, PageSize: 10, Keyword: "7788"})
	if err != nil {
		t.Fatal(err)
	}
	if supplierTotal != 1 || supplierGroups[0].SalesOrderNo != order.OrderNo {
		t.Fatalf("supplier order keyword result total=%d groups=%+v", supplierTotal, supplierGroups)
	}
	assertTeamGroupIDs(t, supplierGroups, futureOne.ID)

	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 1, PageSize: 10, Keyword: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("literal wildcard keyword total=%d want=1", total)
	}
	assertTeamGroupIDs(t, groups, futureTwo.ID)

	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 1, PageSize: 10, Status: "CONFIRMED"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("status total=%d want=2", total)
	}
	assertTeamGroupIDs(t, groups, current.ID, futureTwo.ID)

	start, end := today.AddDate(0, 0, 1), today.AddDate(0, 0, 2)
	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{
		Page: 1, PageSize: 10, VisitStart: &start, VisitEnd: &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("visit range total=%d want=2", total)
	}
	assertTeamGroupIDs(t, groups, futureOne.ID, futureTwo.ID)

	groups, total, err = service.ListGroupsWithOptions(fixture.travel.ID, TeamGroupListOptions{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("paged total=%d want=5", total)
	}
	assertTeamGroupIDs(t, groups, futureTwo.ID, pastOne.ID)

	legacyGroups, legacyTotal, err := service.ListGroups(fixture.travel.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if legacyTotal != 5 {
		t.Fatalf("legacy total=%d want=5", legacyTotal)
	}
	assertTeamGroupIDs(t, legacyGroups, current.ID, futureOne.ID, futureTwo.ID, pastOne.ID, pastTwo.ID)
}
