package service

import (
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestPureTravelAgencyCanEstablishSupplierPartnership(t *testing.T) {
	resetBusinessData(t)
	var supplier, otherSupplier, travelAgency, ordinaryTenant model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		supplier = model.Tenant{Name: "景区供应商", SystemCode: "TRAVEL-SUPPLIER", Status: "active"}
		otherSupplier = model.Tenant{Name: "其他景区", SystemCode: "TRAVEL-OTHER", Status: "active"}
		travelAgency = model.Tenant{Name: "测试旅行社", SystemCode: "TRAVEL-AGENCY", Status: "active"}
		ordinaryTenant = model.Tenant{Name: "普通租户", SystemCode: "TRAVEL-ORDINARY", Status: "active"}
		for _, tenant := range []*model.Tenant{&supplier, &otherSupplier, &travelAgency, &ordinaryTenant} {
			if err := tx.Create(tenant).Error; err != nil {
				return err
			}
		}
		return tx.Create(&[]model.TenantCapability{
			{TenantID: supplier.ID, Capability: "supplier", Status: "active"},
			{TenantID: otherSupplier.ID, Capability: "supplier", Status: "active"},
			{TenantID: travelAgency.ID, Capability: "travel_agency", Status: "active"},
		}).Error
	}); err != nil {
		t.Fatal(err)
	}

	team := &TeamService{}
	found, err := team.SearchSupplierPartner(travelAgency.ID, supplier.SystemCode)
	if err != nil || found.SupplierTenantID != supplier.ID {
		t.Fatalf("supplier search=%+v err=%v", found, err)
	}
	if err := team.ApplySupplierPartnerAudited(travelAgency.ID, 21, "admin", supplier.SystemCode); err != nil {
		t.Fatalf("pure travel agency application failed: %v", err)
	}
	partners, err := team.ListSupplierPartners(travelAgency.ID)
	if err != nil || len(partners) != 1 || partners[0].Status != "pending" {
		t.Fatalf("travel supplier partners=%+v err=%v", partners, err)
	}
	agencies, err := team.ListTravelAgencyPartners(supplier.ID)
	if err != nil || len(agencies) != 1 || agencies[0].TravelTenantID != travelAgency.ID {
		t.Fatalf("supplier travel partners=%+v err=%v", agencies, err)
	}

	if err := team.ApplySupplierPartner(ordinaryTenant.ID, supplier.SystemCode); err == nil {
		t.Fatal("tenant without travel agency capability created a partnership")
	}
	if err := team.AuditTravelAgencyPartner(otherSupplier.ID, agencies[0].RelationshipID, "active"); err == nil {
		t.Fatal("another supplier approved a travel agency partnership")
	}
	if err := team.AuditTravelAgencyPartnerAudited(supplier.ID, agencies[0].RelationshipID, 22, "admin", "active"); err != nil {
		t.Fatalf("supplier approval failed: %v", err)
	}

	contractPartners, err := team.ListContractPartners(supplier.ID)
	if err != nil || len(contractPartners) != 1 || contractPartners[0].TenantID != travelAgency.ID {
		t.Fatalf("contract partners=%+v err=%v", contractPartners, err)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", travelAgency.ID, supplier.ID).First(&account).Error; err != nil {
		t.Fatalf("travel partnership capital account missing: %v", err)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("action IN ?", []string{"team.partner.apply", "team.partner.audit"}).Count(&auditCount).Error; err != nil || auditCount != 2 {
		t.Fatalf("travel partnership audits=%d err=%v", auditCount, err)
	}
}

func TestTravelPartnershipDoesNotAuthorizeDistribution(t *testing.T) {
	resetBusinessData(t)
	var supplier, partner model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		supplier = model.Tenant{Name: "组合能力景区", SystemCode: "DUAL-SUPPLIER", Status: "active"}
		partner = model.Tenant{Name: "组合能力合作方", SystemCode: "DUAL-PARTNER", Status: "active"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&partner).Error; err != nil {
			return err
		}
		return tx.Create(&[]model.TenantCapability{
			{TenantID: supplier.ID, Capability: "supplier", Status: "active"},
			{TenantID: partner.ID, Capability: "travel_agency", Status: "active"},
			{TenantID: partner.ID, Capability: "distributor", Status: "active"},
		}).Error
	}); err != nil {
		t.Fatal(err)
	}

	team := &TeamService{}
	if err := team.ApplySupplierPartner(partner.ID, supplier.SystemCode); err != nil {
		t.Fatal(err)
	}
	travelPartners, err := team.ListTravelAgencyPartners(supplier.ID)
	if err != nil || len(travelPartners) != 1 {
		t.Fatalf("travel applications=%+v err=%v", travelPartners, err)
	}
	if err := team.AuditTravelAgencyPartner(supplier.ID, travelPartners[0].RelationshipID, "active"); err != nil {
		t.Fatal(err)
	}

	distributionSuppliers, err := (&DistributionService{}).ListSuppliers(partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(distributionSuppliers) != 0 {
		t.Fatalf("travel approval leaked into distribution: %+v", distributionSuppliers)
	}
	if err := (&DistributionService{}).ApplyAgent(partner.ID, supplier.SystemCode); err != nil {
		t.Fatal(err)
	}
	var relationship model.DistributorRelationship
	if err := model.DB.Where("agent_tenant_id = ? AND supplier_tenant_id = ?", partner.ID, supplier.ID).First(&relationship).Error; err != nil {
		t.Fatal(err)
	}
	if relationship.TravelStatus != "active" || relationship.Status != "pending" {
		t.Fatalf("independent relationship statuses not preserved: %+v", relationship)
	}
}
