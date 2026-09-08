package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestOnlineOrderHistorySurvivesChannelEnvironmentSwitch(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	svc := &OrderService{}
	orders, _, err := svc.ListWithSalesScope(1, 10, fixture.tenantID, "", "xiaohongshu", "online", "", "", fixture.order.OrderNo)
	if err != nil || len(orders) != 1 {
		t.Fatalf("production order missing before account change: %v %v", orders, err)
	}
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).
		Updates(map[string]interface{}{"environment": "sandbox", "status": "sandbox"}).Error; err != nil {
		t.Fatal(err)
	}
	orders, _, err = svc.ListWithSalesScope(1, 10, fixture.tenantID, "", "xiaohongshu", "online", "", "", fixture.order.OrderNo)
	if err != nil || len(orders) != 1 {
		t.Fatalf("historical production order hidden by current channel environment: %v %v", orders, err)
	}
}

func TestOnlineOrderSalesSourceSurvivesTeamAssociation(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", fixture.tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TourGroup{TenantID: fixture.tenantID, SupplierTenantID: fixture.tenantID,
		ScenicAreaID: area.ID, SalesOrderID: fixture.order.ID, GroupNo: "ONLINE-SOURCE-TEAM", Name: "线上订单关联团队",
		VisitDate: time.Now(), ExpectedCount: 1, Status: "confirmed"}).Error; err != nil {
		t.Fatal(err)
	}
	orders, _, err := (&OrderService{}).ListWithSalesScope(1, 10, fixture.tenantID, "", "xiaohongshu", "online", "", "", fixture.order.OrderNo)
	if err != nil || len(orders) != 1 {
		t.Fatalf("team association must not rewrite online sales source: %v %v", orders, err)
	}
}

func TestOnlineXiaohongshuSourceLabelDoesNotImplyOneAccount(t *testing.T) {
	// Filtering by the shared channel value includes all XHS accounts, so a
	// label naming one arbitrarily selected account would be misleading.
	if got := orderChannelLabel("xiaohongshu", "xiaohongshu", "account-A"); got != "小红书" {
		t.Fatalf("shared channel label = %q", got)
	}
}
