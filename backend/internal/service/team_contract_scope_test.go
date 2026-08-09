package service

import (
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
