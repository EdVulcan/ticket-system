package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"
)

func seedTeamReferenceActor(t *testing.T, fixture teamP0Fixture) model.User {
	t.Helper()
	actor := model.User{TenantID: fixture.travel.ID, Username: "team-reference-admin", Password: "test", Role: "team_operator"}
	if err := model.DB.Create(&actor).Error; err != nil {
		t.Fatalf("create team reference actor: %v", err)
	}
	return actor
}

func TestTeamReferenceRecordsCanBeMaintainedAndAudited(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	actor := seedTeamReferenceActor(t, fixture)
	service := &TeamService{}

	agent, err := service.CreateAgent(fixture.travel.ID, actor.ID, TeamAgentInput{
		Name: "  张业务  ", Phone: " 13800138000 ", JobNumber: " TEAM-001 ",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if agent.TenantID != fixture.travel.ID || agent.Name != "张业务" || agent.Phone != "13800138000" || agent.JobNumber != "TEAM-001" || agent.Status != "active" {
		t.Fatalf("agent was not normalized or scoped: %+v", agent)
	}
	agent, err = service.UpdateAgent(fixture.travel.ID, agent.ID, actor.ID, TeamAgentInput{
		Name: "张业务员", Phone: "13900139000", JobNumber: "TEAM-002",
	})
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if agent.Name != "张业务员" || agent.JobNumber != "TEAM-002" || agent.Status != "active" {
		t.Fatalf("unexpected updated agent: %+v", agent)
	}
	agent, err = service.SetAgentStatus(fixture.travel.ID, agent.ID, actor.ID, "inactive", "人员离职")
	if err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	if agent.Status != "inactive" {
		t.Fatalf("agent status=%s, want inactive", agent.Status)
	}

	guide, err := service.CreateGuide(fixture.travel.ID, actor.ID, TeamGuideInput{
		Name: "  李导游 ", Phone: " 13700137000 ", LicenseNo: " GUIDE-001 ",
	})
	if err != nil {
		t.Fatalf("create guide: %v", err)
	}
	guide, err = service.UpdateGuide(fixture.travel.ID, guide.ID, actor.ID, TeamGuideInput{
		Name: "李导", Phone: "13700137001", LicenseNo: "GUIDE-002",
	})
	if err != nil {
		t.Fatalf("update guide: %v", err)
	}
	guide, err = service.SetGuideStatus(fixture.travel.ID, guide.ID, actor.ID, "inactive", "暂不带团")
	if err != nil {
		t.Fatalf("disable guide: %v", err)
	}
	if guide.Name != "李导" || guide.LicenseNo != "GUIDE-002" || guide.Status != "inactive" {
		t.Fatalf("unexpected guide: %+v", guide)
	}

	vehicle, err := service.CreateVehicle(fixture.travel.ID, actor.ID, TeamVehicleInput{
		PlateNumber: " 粤a12345 ", DriverName: "  王师傅 ", DriverPhone: " 13600136000 ", Capacity: 48,
	})
	if err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	if vehicle.PlateNumber != "粤A12345" || vehicle.DriverName != "王师傅" {
		t.Fatalf("vehicle was not normalized: %+v", vehicle)
	}
	vehicle, err = service.UpdateVehicle(fixture.travel.ID, vehicle.ID, actor.ID, TeamVehicleInput{
		PlateNumber: "粤B54321", DriverName: "赵师傅", DriverPhone: "13500135000", Capacity: 53,
	})
	if err != nil {
		t.Fatalf("update vehicle: %v", err)
	}
	vehicle, err = service.SetVehicleStatus(fixture.travel.ID, vehicle.ID, actor.ID, "inactive", "车辆检修")
	if err != nil {
		t.Fatalf("disable vehicle: %v", err)
	}
	if vehicle.PlateNumber != "粤B54321" || vehicle.Capacity != 53 || vehicle.Status != "inactive" {
		t.Fatalf("unexpected vehicle: %+v", vehicle)
	}

	for _, action := range []string{
		"team.agent.create", "team.agent.update", "team.agent.status",
		"team.guide.create", "team.guide.update", "team.guide.status",
		"team.vehicle.create", "team.vehicle.update", "team.vehicle.status",
	} {
		var count int64
		if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND actor_user_id = ? AND action = ?", fixture.travel.ID, actor.ID, action).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("audit action %s count=%d, want 1", action, count)
		}
	}
}

func TestTeamReferenceManagementRejectsForeignTenantAndInactiveCapability(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	actor := seedTeamReferenceActor(t, fixture)
	service := &TeamService{}

	foreignGuide := model.TourGuide{TenantID: fixture.supplier.ID, Name: "其他租户导游", Status: "active"}
	if err := model.DB.Create(&foreignGuide).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateGuide(fixture.travel.ID, foreignGuide.ID, actor.ID, TeamGuideInput{Name: "越权修改"}); err == nil {
		t.Fatal("foreign guide was updated")
	}
	if _, err := service.SetGuideStatus(fixture.travel.ID, foreignGuide.ID, actor.ID, "inactive", "越权停用"); err == nil {
		t.Fatal("foreign guide status was updated")
	}
	var storedForeign model.TourGuide
	if err := model.DB.First(&storedForeign, foreignGuide.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedForeign.Name != "其他租户导游" || storedForeign.Status != "active" {
		t.Fatalf("foreign guide changed: %+v", storedForeign)
	}

	ownGuide, err := service.CreateGuide(fixture.travel.ID, actor.ID, TeamGuideInput{Name: "本社导游"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.travel.ID, "travel_agency").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateGuide(fixture.travel.ID, ownGuide.ID, actor.ID, TeamGuideInput{Name: "能力停用后修改"}); err == nil {
		t.Fatal("suspended travel capability could update guide")
	}
	if _, err := service.SetGuideStatus(fixture.travel.ID, ownGuide.ID, actor.ID, "inactive", "能力停用"); err == nil {
		t.Fatal("suspended travel capability could disable guide")
	}
	if _, err := service.CreateVehicle(fixture.travel.ID, actor.ID, TeamVehicleInput{PlateNumber: "粤A00001"}); err == nil {
		t.Fatal("suspended travel capability could create vehicle")
	}
}

func TestTeamReferenceManagementValidatesFieldsAndStatus(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	actor := seedTeamReferenceActor(t, fixture)
	service := &TeamService{}

	if _, err := service.CreateAgent(fixture.travel.ID, actor.ID, TeamAgentInput{Name: "业务员", JobNumber: strings.Repeat("A", 51)}); err == nil {
		t.Fatal("oversized job number was accepted")
	}
	if _, err := service.CreateGuide(fixture.travel.ID, actor.ID, TeamGuideInput{Name: "包含\n控制符"}); err == nil {
		t.Fatal("control character in guide name was accepted")
	}
	if _, err := service.CreateVehicle(fixture.travel.ID, actor.ID, TeamVehicleInput{PlateNumber: "粤A00002", Capacity: -1}); err == nil {
		t.Fatal("negative vehicle capacity was accepted")
	}

	agent, err := service.CreateAgent(fixture.travel.ID, actor.ID, TeamAgentInput{Name: "业务员", JobNumber: "TEAM-STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAgentStatus(fixture.travel.ID, agent.ID, actor.ID, "deleted", "非法状态"); err == nil {
		t.Fatal("invalid agent status was accepted")
	}
	if _, err := service.UpdateAgent(fixture.travel.ID, agent.ID, actor.ID, TeamAgentInput{Name: "", JobNumber: "TEAM-STATUS"}); err == nil {
		t.Fatal("empty agent name was accepted")
	}

	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action LIKE ?", fixture.travel.ID, "team.%.status").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("rejected status change wrote %d audit rows", auditCount)
	}
}
