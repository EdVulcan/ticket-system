package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestTravelContractNumberIsScopedToSupplierAndTravelAgency(t *testing.T) {
	resetBusinessData(t)
	var travel model.Tenant
	var firstSupplier model.Tenant
	var secondSupplier model.Tenant
	err := model.Write(func(tx *gorm.DB) error {
		travel = model.Tenant{Name: "合同旅行社", SystemCode: "CONTRACT-SCOPE-TRAVEL", Status: "active"}
		firstSupplier = model.Tenant{Name: "甲景区", SystemCode: "CONTRACT-SCOPE-S1", Status: "active"}
		secondSupplier = model.Tenant{Name: "乙景区", SystemCode: "CONTRACT-SCOPE-S2", Status: "active"}
		if err := tx.Create(&travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&firstSupplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&secondSupplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TravelContract{
			TravelTenantID: travel.ID, SupplierTenantID: firstSupplier.ID,
			ContractNo: "2026-TEAM-001", Status: "active",
		}).Error; err != nil {
			return err
		}
		return tx.Create(&model.TravelContract{
			TravelTenantID: travel.ID, SupplierTenantID: secondSupplier.ID,
			ContractNo: "2026-TEAM-001", Status: "active",
		}).Error
	})
	if err != nil {
		t.Fatalf("different suppliers could not reuse their own contract number: %v", err)
	}
}

func TestTeamContractProductsExcludePoliciesUnsupportedByTeamRoster(t *testing.T) {
	resetBusinessData(t)
	var supplier model.Tenant
	var unrestricted model.Product
	var regionRestricted model.Product
	err := model.Write(func(tx *gorm.DB) error {
		supplier = model.Tenant{Name: "团队合同景区", SystemCode: "TEAM-POLICY-SUPPLIER", Status: "active"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: supplier.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: supplier.ID, Code: "TEAM-POLICY-AREA", Name: "团队合同景区", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		unrestricted = model.Product{TenantID: supplier.ID, ScenicAreaID: area.ID, Name: "团队可售票", Type: "online", Status: "online", CodeMode: "ticket"}
		regionRestricted = model.Product{TenantID: supplier.ID, ScenicAreaID: area.ID, Name: "地区限制票", Type: "online", Status: "online", CodeMode: "ticket", RegionLimit: `["4402"]`}
		return tx.Create(&[]model.Product{unrestricted, regionRestricted}).Error
	})
	if err != nil {
		t.Fatal(err)
	}

	products, err := (&TeamService{}).ListContractProducts(supplier.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0].Name != unrestricted.Name {
		t.Fatalf("contract products=%+v, want only unrestricted product %q", products, unrestricted.Name)
	}

	if err := model.DB.Where("tenant_id = ? AND name = ?", supplier.ID, regionRestricted.Name).First(&regionRestricted).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err = normalizeTeamPriceRulesTx(model.DB, supplier.ID, []TeamPriceRule{{ProductID: regionRestricted.ID, PriceCents: 100}})
	if err == nil || !strings.Contains(err.Error(), "当前不可用") {
		t.Fatalf("region-restricted contract rule error=%v", err)
	}
}
