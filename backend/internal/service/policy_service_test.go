package service

import (
	"testing"
	"ticket-backend/internal/model"
)

func TestEmptyRegionPolicyDoesNotRequireVisitorRegion(t *testing.T) {
	if err := validateRegion(`[]`, ""); err != nil {
		t.Fatalf("empty region policy rejected an unspecified visitor region: %v", err)
	}
	if err := validateRegion(` [ ] `, "CN"); err != nil {
		t.Fatalf("empty region policy rejected a supplied visitor region: %v", err)
	}
}

func TestPolicyListHidesInactiveFromSellerButKeepsAdminMaintenance(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "政策景区", SystemCode: "POLICY-TEST", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	active := model.Policy{TenantID: tenant.ID, Category: "Admission", Title: "儿童免票", Content: "身高符合条件可免票", IsActive: true}
	inactive := model.Policy{TenantID: tenant.ID, Category: "Refund", Title: "旧退票规则", Content: "已停用", IsActive: false}
	svc := &PolicyService{}
	if err := svc.Create(&active); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(&inactive); err != nil {
		t.Fatal(err)
	}
	sellerRows, err := svc.List(tenant.ID, "")
	if err != nil || len(sellerRows) != 1 || sellerRows[0].ID != active.ID {
		t.Fatalf("seller policies=%+v err=%v", sellerRows, err)
	}
	adminRows, err := svc.List(tenant.ID, "", true)
	if err != nil || len(adminRows) != 2 {
		t.Fatalf("admin policies=%+v err=%v", adminRows, err)
	}
}
