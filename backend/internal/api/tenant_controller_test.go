package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTenantRequestsIgnoreLifecycleFields(t *testing.T) {
	createPayload := []byte(`{
		"name":"山河旅行社",
		"system_code":"SHLX001",
		"admin_password":"Travel-Password-123",
		"qualification_expires_at":"",
		"contract_expires_at":"",
		"qualification_status":"approved"
	}`)
	var create CreateTenantRequest
	if err := json.Unmarshal(createPayload, &create); err != nil {
		t.Fatalf("create request rejected legacy lifecycle fields: %v", err)
	}
	if create.Name != "山河旅行社" || create.SystemCode != "SHLX001" {
		t.Fatalf("unexpected create request: %+v", create)
	}

	updatePayload := []byte(`{
		"name":"山河旅行社",
		"qualification_expires_at":"",
		"contract_expires_at":""
	}`)
	var update UpdateTenantRequest
	if err := json.Unmarshal(updatePayload, &update); err != nil {
		t.Fatalf("update request rejected legacy lifecycle fields: %v", err)
	}
	if update.Name != "山河旅行社" {
		t.Fatalf("unexpected update request: %+v", update)
	}
}

func TestTenantStatusActivationPreconditionIsConflict(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}); err != nil {
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

	tenant := model.Tenant{Name: "测试旅行社", SystemCode: "TRAVEL002", Status: "frozen", QualificationStatus: "pending"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/2/status", bytes.NewBufferString(`{"status":"active"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(tenant.ID), 10)}}
	ctx.Set("platform_user_id", uint(9))
	ctx.Set("role", "platform_admin")
	(&TenantController{Service: service.TenantService{}}).UpdateStatus(ctx)
	if response.Code != http.StatusConflict {
		t.Fatalf("activation response=%d body=%s", response.Code, response.Body.String())
	}
}
