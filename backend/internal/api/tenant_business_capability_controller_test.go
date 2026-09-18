package api

import (
	"bytes"
	"context"
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

func TestSetBusinessCapability(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantBusinessCapability{}, &model.AuditLog{}); err != nil {
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
	tenant := model.Tenant{Name: "餐饮租户", SystemCode: "API-COMMERCE-CAPABILITY", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPut, "/tenants/"+strconv.FormatUint(uint64(tenant.ID), 10)+"/business-capabilities/restaurant", bytes.NewBufferString(`{"status":"active","reason":"platform approval"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(tenant.ID), 10)}, {Key: "businessType", Value: "restaurant"}}
	ctx.Set("platform_user_id", uint(9))
	ctx.Set("role", "platform_admin")
	(&TenantController{Service: service.TenantService{}}).SetBusinessCapability(ctx)
	if response.Code != http.StatusOK {
		t.Fatalf("success response=%d body=%s", response.Code, response.Body.String())
	}
	var row model.TenantBusinessCapability
	if err := db.Where("tenant_id = ? AND business_type = ?", tenant.ID, "restaurant").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "active" {
		t.Fatalf("business capability status=%q", row.Status)
	}

	badRequest := httptest.NewRequest(http.MethodPut, "/tenants/"+strconv.FormatUint(uint64(tenant.ID), 10)+"/business-capabilities/hotel", bytes.NewBufferString(`{"status":"active","reason":"platform approval"}`))
	badRequest.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	badCtx, _ := gin.CreateTestContext(badResponse)
	badCtx.Request = badRequest
	badCtx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(tenant.ID), 10)}, {Key: "businessType", Value: "hotel"}}
	badCtx.Set("platform_user_id", uint(9))
	badCtx.Set("role", "platform_admin")
	(&TenantController{Service: service.TenantService{}}).SetBusinessCapability(badCtx)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid business type response=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
}
