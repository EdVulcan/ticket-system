package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func seedTeamPlanAssignments(t *testing.T, fixture teamP0Fixture) (model.TravelAgent, model.TourGuide, model.TravelVehicle, model.User) {
	t.Helper()
	var agent model.TravelAgent
	var guide model.TourGuide
	var vehicle model.TravelVehicle
	var actor model.User
	err := model.Write(func(tx *gorm.DB) error {
		agent = model.TravelAgent{TenantID: fixture.travel.ID, Name: "团队业务员", JobNumber: "TEAM-AGENT", Status: "active"}
		if err := tx.Create(&agent).Error; err != nil {
			return err
		}
		guide = model.TourGuide{TenantID: fixture.travel.ID, Name: "测试导游", Status: "active"}
		if err := tx.Create(&guide).Error; err != nil {
			return err
		}
		vehicle = model.TravelVehicle{TenantID: fixture.travel.ID, PlateNumber: "粤A12345", Status: "active"}
		if err := tx.Create(&vehicle).Error; err != nil {
			return err
		}
		actor = model.User{TenantID: fixture.travel.ID, Username: "team-plan-operator", Password: "test", Role: "team_operator"}
		return tx.Create(&actor).Error
	})
	if err != nil {
		t.Fatalf("seed team plan assignments: %v", err)
	}
	return agent, guide, vehicle, actor
}

func TestUpdateTeamGroupPlanAllowsDraftDetailsAndAudits(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	agent, guide, vehicle, actor := seedTeamPlanAssignments(t, fixture)
	group := createTeamP0Group(t, fixture, "原团队名称", 1)
	newName := "更新后的团队名称"
	newVisitDate := group.VisitDate.Add(24 * time.Hour)

	updated, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{
		Name: &newName, VisitDate: &newVisitDate, AgentID: &agent.ID, GuideID: &guide.ID, VehicleID: &vehicle.ID,
	})
	if err != nil {
		t.Fatalf("update draft team plan: %v", err)
	}
	if updated.Name != newName || !sameTeamDate(updated.VisitDate, newVisitDate) || updated.AgentID != agent.ID || updated.GuideID != guide.ID || updated.VehicleID != vehicle.ID {
		t.Fatalf("unexpected updated plan: %+v", updated)
	}
	if updated.SupplierTenantID != fixture.supplier.ID || updated.ScenicAreaID != fixture.area.ID || updated.ContractID != fixture.contract.ID {
		t.Fatalf("immutable fulfillment scope changed: %+v", updated)
	}
	var audit model.AuditLog
	if err := model.DB.Where("tenant_id = ? AND action = ? AND target_type = ? AND target_id = ?", fixture.travel.ID, "team.plan.update", "tour_group", group.ID).First(&audit).Error; err != nil {
		t.Fatalf("load update audit: %v", err)
	}
}

func TestUpdateTeamGroupPlanProtectsVisitDateAfterConfirmation(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	_, firstGuide, _, actor := seedTeamPlanAssignments(t, fixture)
	secondGuide := model.TourGuide{TenantID: fixture.travel.ID, Name: "替换导游", Status: "active"}
	if err := model.DB.Create(&secondGuide).Error; err != nil {
		t.Fatal(err)
	}
	group := createTeamP0Group(t, fixture, "已确认计划", 1)
	if err := model.DB.Create(&model.TourGroupConfirmation{
		GroupID: group.ID, Sequence: 1, TravelTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID,
		ScenicAreaID: fixture.area.ID, ConfirmedCount: 1, GuideID: firstGuide.ID, SubmittedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	newVisitDate := group.VisitDate.Add(24 * time.Hour)
	if _, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{VisitDate: &newVisitDate, Reason: "客户调整日期"}); err == nil {
		t.Fatal("confirmed team visit date was changed and invalidated its confirmation snapshot")
	}
	updated, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{GuideID: &secondGuide.ID, Reason: "现场更换导游"})
	if err != nil {
		t.Fatalf("update confirmed operational assignment: %v", err)
	}
	if updated.GuideID != secondGuide.ID || !sameTeamDate(updated.VisitDate, group.VisitDate) {
		t.Fatalf("unexpected confirmed plan update: %+v", updated)
	}
}

func TestUpdateTeamGroupPlanRejectsForeignAssignments(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	_, _, _, actor := seedTeamPlanAssignments(t, fixture)
	foreignGuide := model.TourGuide{TenantID: fixture.supplier.ID, Name: "其他租户导游", Status: "active"}
	if err := model.DB.Create(&foreignGuide).Error; err != nil {
		t.Fatal(err)
	}
	group := createTeamP0Group(t, fixture, "归属边界测试", 0)
	if _, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{GuideID: &foreignGuide.ID}); err == nil {
		t.Fatal("team plan accepted a guide owned by another tenant")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.GuideID != 0 {
		t.Fatalf("foreign guide was persisted: %+v", stored)
	}
}

func TestCreateTeamGroupRejectsForeignOrInactiveAssignments(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	foreignGuide := model.TourGuide{TenantID: fixture.supplier.ID, Name: "Foreign guide", Status: "active"}
	inactiveVehicle := model.TravelVehicle{TenantID: fixture.travel.ID, PlateNumber: "TEST-INACTIVE", Status: "inactive"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&foreignGuide).Error; err != nil {
			return err
		}
		return tx.Create(&inactiveVehicle).Error
	}); err != nil {
		t.Fatal(err)
	}

	base := func() model.TourGroup {
		return model.TourGroup{
			Name: "Assignment boundary team", SupplierTenantID: fixture.supplier.ID,
			ScenicAreaID: fixture.area.ID, ContractID: fixture.contract.ID,
			VisitDate: time.Now().Add(24 * time.Hour), ExpectedCount: 1,
		}
	}
	service := &TeamService{}
	withForeignGuide := base()
	withForeignGuide.GuideID = foreignGuide.ID
	if err := service.CreateGroup(fixture.travel.ID, &withForeignGuide); err == nil {
		t.Fatal("team creation accepted a guide owned by another tenant")
	}
	withInactiveVehicle := base()
	withInactiveVehicle.VehicleID = inactiveVehicle.ID
	if err := service.CreateGroup(fixture.travel.ID, &withInactiveVehicle); err == nil {
		t.Fatal("team creation accepted an inactive vehicle")
	}
	var count int64
	if err := model.DB.Model(&model.TourGroup{}).Where("name = ?", "Assignment boundary team").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid assignment persisted %d teams", count)
	}
}

func TestTeamGroupRequiresPlannedHeadcountAndRosterDoesNotDoubleIt(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	service := &TeamService{}
	invalid := model.TourGroup{
		Name: "Missing headcount", SupplierTenantID: fixture.supplier.ID,
		ScenicAreaID: fixture.area.ID, ContractID: fixture.contract.ID,
		VisitDate: time.Now().Add(24 * time.Hour),
	}
	if err := service.CreateGroup(fixture.travel.ID, &invalid); err == nil {
		t.Fatal("team creation accepted a zero planned headcount")
	}

	group := invalid
	group.Name = "Planned headcount"
	group.ExpectedCount = 2
	if err := service.CreateGroup(fixture.travel.ID, &group); err != nil {
		t.Fatalf("create team with planned headcount: %v", err)
	}
	if _, err := service.AddMembers(fixture.travel.ID, group.ID, []model.TourGroupMember{{Name: "Alice"}}); err != nil {
		t.Fatalf("add first planned member: %v", err)
	}
	if _, err := service.AddMembers(fixture.travel.ID, group.ID, []model.TourGroupMember{{Name: "Bob"}}); err != nil {
		t.Fatalf("add second planned member: %v", err)
	}
	if _, err := service.AddMembers(fixture.travel.ID, group.ID, []model.TourGroupMember{{Name: "Overflow"}}); err == nil {
		t.Fatal("roster exceeded the planned headcount")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ExpectedCount != 2 {
		t.Fatalf("planned headcount=%d, want 2", stored.ExpectedCount)
	}
	var memberCount int64
	if err := model.DB.Model(&model.TourGroupMember{}).Where("group_id = ?", group.ID).Count(&memberCount).Error; err != nil {
		t.Fatal(err)
	}
	if memberCount != 2 {
		t.Fatalf("member count=%d, want 2", memberCount)
	}
}

func TestReplaceTeamRosterRejectsEmptyWithoutDestroyingPlannedHeadcount(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "Keep planned headcount", 2)
	if _, err := (&TeamService{}).ReplaceMembers(fixture.travel.ID, group.ID, nil); err == nil {
		t.Fatal("empty replacement roster was accepted")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ExpectedCount != 2 {
		t.Fatalf("planned headcount=%d, want 2", stored.ExpectedCount)
	}
	var memberCount int64
	if err := model.DB.Model(&model.TourGroupMember{}).Where("group_id = ?", group.ID).Count(&memberCount).Error; err != nil {
		t.Fatal(err)
	}
	if memberCount != 2 {
		t.Fatalf("member count=%d, want 2", memberCount)
	}
}

func TestUpdateTeamGroupPlanAllowsReasonedPartialEntryAssignmentAndRejectsFinishedGroup(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	_, guide, _, actor := seedTeamPlanAssignments(t, fixture)
	group := createTeamP0Group(t, fixture, "部分入园团队", 1)
	if err := model.DB.Model(&model.TourGroup{}).Where("id = ?", group.ID).Update("status", "partial_entry").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{GuideID: &guide.ID}); err == nil {
		t.Fatal("partial-entry plan assignment changed without an audit reason")
	}
	updated, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{GuideID: &guide.ID, Reason: "剩余游客改由新导游带队"})
	if err != nil {
		t.Fatalf("update partial-entry assignment: %v", err)
	}
	if updated.GuideID != guide.ID {
		t.Fatalf("guide id=%d, want %d", updated.GuideID, guide.ID)
	}
	if err := model.DB.Model(&model.TourGroup{}).Where("id = ?", group.ID).Update("status", "entered").Error; err != nil {
		t.Fatal(err)
	}
	clearedGuide := uint(0)
	if _, err := (&TeamService{}).UpdateGroupPlan(fixture.travel.ID, group.ID, actor.ID, TeamGroupPlanUpdate{GuideID: &clearedGuide, Reason: "尝试修改"}); err == nil {
		t.Fatal("fully entered team accepted a plan update")
	}
}

func TestTeamGroupLifecycleRejectsUnrelatedTravelTenant(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "租户隔离团队", 0)
	var foreignTenant model.Tenant
	var foreignUser model.User
	if err := model.Write(func(tx *gorm.DB) error {
		foreignTenant = model.Tenant{Name: "无关旅行社", SystemCode: "FOREIGN-TEAM-TENANT", SecretKey: "x", Status: "active"}
		if err := tx.Create(&foreignTenant).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: foreignTenant.ID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
			return err
		}
		foreignUser = model.User{TenantID: foreignTenant.ID, Username: "foreign-team-user", Password: "test", Role: "team_operator"}
		return tx.Create(&foreignUser).Error
	}); err != nil {
		t.Fatal(err)
	}
	newName := "越权修改"
	service := &TeamService{}
	if _, err := service.UpdateGroupPlan(foreignTenant.ID, group.ID, foreignUser.ID, TeamGroupPlanUpdate{Name: &newName}); err == nil {
		t.Fatal("unrelated travel tenant updated another tenant's team")
	}
	if _, err := service.CancelGroup(foreignTenant.ID, group.ID, foreignUser.ID, "越权取消"); err == nil {
		t.Fatal("unrelated travel tenant cancelled another tenant's team")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Name != group.Name || stored.Status != "draft" {
		t.Fatalf("team changed after cross-tenant attempts: %+v", stored)
	}
}

func TestCancelTeamGroupRequiresReasonPreservesRosterAndIsIdempotent(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	_, _, _, actor := seedTeamPlanAssignments(t, fixture)
	group := createTeamP0Group(t, fixture, "待取消团队", 2)
	service := &TeamService{}
	if _, err := service.CancelGroup(fixture.travel.ID, group.ID, actor.ID, ""); err == nil {
		t.Fatal("team cancellation accepted an empty reason")
	}
	cancelled, err := service.CancelGroup(fixture.travel.ID, group.ID, actor.ID, "旅行社取消行程")
	if err != nil {
		t.Fatalf("cancel safe draft team: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("group status=%s, want cancelled", cancelled.Status)
	}
	var members []model.TourGroupMember
	if err := model.DB.Where("group_id = ?", group.ID).Order("id").Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].Status != "cancelled" || members[1].Status != "cancelled" {
		t.Fatalf("roster facts were deleted or left active: %+v", members)
	}
	if _, err := service.CancelGroup(fixture.travel.ID, group.ID, actor.ID, "网络重试"); err != nil {
		t.Fatalf("retry cancellation should be idempotent: %v", err)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ? AND target_id = ?", fixture.travel.ID, "team.plan.cancel", group.ID).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("cancellation audit count=%d, want 1", auditCount)
	}
}

func TestCancelTeamGroupRejectsBoundOrderWithoutChangingFacts(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	_, _, _, actor := seedTeamPlanAssignments(t, fixture)
	group := createTeamP0Group(t, fixture, "已绑定订单团队", 1)
	if err := (&TeamService{}).AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := (&TeamService{}).CancelGroup(fixture.travel.ID, group.ID, actor.ID, "行程取消"); err == nil {
		t.Fatal("bound team was cancelled without order after-sale handling")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "confirmed" || stored.SalesOrderID != order.ID {
		t.Fatalf("bound group facts changed after rejected cancellation: %+v", stored)
	}
	var member model.TourGroupMember
	if err := model.DB.Where("group_id = ?", group.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if member.Status != "ticketed" || member.TicketCode == "" {
		t.Fatalf("ticket assignment changed after rejected cancellation: %+v", member)
	}
}
