package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTeamOperatorAdmissionUsesTeamDeviceOwnershipInsteadOfStaffScope(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantCapability{}, &model.SupplierBusinessType{}, &model.TourGroup{}, &model.StaffResourceScope{}); err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = nil
	})

	tenant := model.Tenant{Name: "团队履约景区", SystemCode: "TEAM-ENTRY-ACCESS", SecretKey: "test", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SupplierBusinessType{TenantID: tenant.ID, BusinessType: "scenic", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/teams/999999/enter-batch", bytes.NewBufferString(`{"device_id":77,"member_ids":[1],"idempotency_key":"team-entry-access"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: "999999"}}
	ctx.Set("tenant_id", tenant.ID)
	ctx.Set("user_id", uint(9001))
	ctx.Set("role", "team_operator")
	(&TeamController{}).EnterBatch(ctx)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "group not found") {
		t.Fatalf("response=%d body=%s, want service ownership validation instead of staff-scope 403", response.Code, response.Body.String())
	}
}
