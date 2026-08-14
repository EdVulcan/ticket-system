package service

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type catalogBatchFixture struct {
	tenant     model.Tenant
	area       model.ScenicArea
	product    model.Product
	checkpoint model.CheckPoint
	extra      model.CheckPoint
}

func seedCatalogBatchFixture(t *testing.T) catalogBatchFixture {
	t.Helper()
	resetBusinessData(t)
	fixture := catalogBatchFixture{}
	if err := model.Write(func(tx *gorm.DB) error {
		fixture.tenant = model.Tenant{Name: "Catalog Batch Tenant", SystemCode: fmt.Sprintf("CATALOG-BATCH-%d", time.Now().UnixNano()), SecretKey: "catalog-secret", Status: "active"}
		if err := tx.Create(&fixture.tenant).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: fixture.tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		if err := seedActiveScenicBusinessTypeTx(tx, fixture.tenant.ID); err != nil {
			return err
		}
		fixture.area = model.ScenicArea{TenantID: fixture.tenant.ID, Code: "CATALOG-BATCH-AREA", Name: "Batch Scenic", Status: "active"}
		if err := tx.Create(&fixture.area).Error; err != nil {
			return err
		}
		fixture.checkpoint = model.CheckPoint{Name: "Main Gate", TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID}
		fixture.extra = model.CheckPoint{Name: "North Gate", TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID}
		if err := tx.Create(&fixture.checkpoint).Error; err != nil {
			return err
		}
		return tx.Create(&fixture.extra).Error
	}); err != nil {
		t.Fatal(err)
	}
	product := model.Product{
		Name: "Adult Ticket", Price: 99.5, SettlementPrice: 60, TenantID: fixture.tenant.ID,
		ScenicAreaID: fixture.area.ID, Type: "online", Status: "online", ValidityType: "date",
		StockType: "unlimited", CodeMode: "ticket", RefundType: "free", GateVoiceCode: "welcome",
	}
	rule := model.TicketRule{Name: "Admission Rule", TenantID: fixture.tenant.ID, ValidityType: "date", Groups: []model.RuleGroup{{GroupName: "Admission", MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: fixture.checkpoint.ID, MaxPerCheckIn: 1}}}}}
	if err := (&ProductService{}).Create(&product, &rule); err != nil {
		t.Fatal(err)
	}
	fixture.product = product
	return fixture
}

func TestCatalogBatchPreviewAndConfirmPreservesHistoryAndOffer(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	oldRevision := fixture.product.CurrentRevisionID
	var oldRevisionRow model.ProductRevision
	if err := model.DB.First(&oldRevisionRow, oldRevision).Error; err != nil {
		t.Fatal(err)
	}
	otherTenant := model.Tenant{Name: "Catalog Batch Distributor", SystemCode: fmt.Sprintf("CATALOG-DIST-%d", time.Now().UnixNano()), SecretKey: "dist-secret", Status: "active"}
	staleTenant := model.Tenant{Name: "Catalog Batch Stale Distributor", SystemCode: fmt.Sprintf("CATALOG-STALE-DIST-%d", time.Now().UnixNano()), SecretKey: "stale-dist-secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&otherTenant).Error; err != nil {
			return err
		}
		if err := tx.Create(&staleTenant).Error; err != nil {
			return err
		}
		// This intentionally models a legacy active offer pointing at an old
		// revision row that is no longer the latest version. Version zero is
		// below the real current version so the test does not change which
		// revision createProductRevisionTx expires next.
		staleRevision := model.ProductRevision{ProductID: fixture.product.ID, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Version: 0, Status: "expired", SnapshotJSON: oldRevisionRow.SnapshotJSON, EffectiveFrom: time.Now().Add(-time.Hour)}
		if err := tx.Create(&staleRevision).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.ProductOffer{SupplierTenantID: fixture.tenant.ID, DistributorTenantID: otherTenant.ID, SourceProductID: fixture.product.ID, ProductRevisionID: oldRevision, FulfillmentScenicAreaID: fixture.area.ID, SettlementPrice: 60, Status: "active", AllowedChannels: "[\"online\"]"}).Error; err != nil {
			return err
		}
		return tx.Create(&model.ProductOffer{SupplierTenantID: fixture.tenant.ID, DistributorTenantID: staleTenant.ID, SourceProductID: fixture.product.ID, ProductRevisionID: staleRevision.ID, FulfillmentScenicAreaID: fixture.area.ID, SettlementPrice: 60, Status: "active", AllowedChannels: "[\"online\"]"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	service := &CatalogBatchChangeService{}
	preview, err := service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		InputText:      "给 Adult Ticket 增加 North Gate 检票点，每个点最多核销 2 次",
		IdempotencyKey: "catalog-batch-history-1",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.PlanID == 0 || !preview.CanConfirm || len(preview.Lines) != 1 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if preview.Operations[0].Kind != CatalogBatchOpAddCheckpoint || len(preview.Operations[0].CheckpointIDs) != 1 || preview.Operations[0].CheckpointIDs[0] != fixture.extra.ID {
		t.Fatalf("natural language was not normalized: %+v", preview.Operations)
	}
	if preview.Operations[0].MaxPerCheckIn == nil || *preview.Operations[0].MaxPerCheckIn != 2 {
		t.Fatalf("natural language checkpoint limit was not normalized: %+v", preview.Operations)
	}
	var productBefore model.Product
	if err := model.DB.First(&productBefore, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if productBefore.CurrentRevisionID != oldRevision {
		t.Fatal("preview changed product revision")
	}

	repeated, err := service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{InputText: "给 Adult Ticket 增加 North Gate 检票点，每个点最多核销 2 次", IdempotencyKey: "catalog-batch-history-1"})
	if err != nil || repeated.PlanID != preview.PlanID {
		t.Fatalf("preview idempotency failed: plan=%+v err=%v", repeated, err)
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", preview.PlanID, preview.PlanHash)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if completed.Status != CatalogBatchPlanCompleted || completed.Lines[0].AfterRevisionID == 0 || completed.Lines[0].AfterRevisionID == oldRevision {
		t.Fatalf("unexpected completion: %+v", completed)
	}
	var revised model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").First(&revised, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if revised.CurrentRevisionID != completed.Lines[0].AfterRevisionID {
		t.Fatalf("product revision=%d, completion revision=%d", revised.CurrentRevisionID, completed.Lines[0].AfterRevisionID)
	}
	if len(revised.Rule.Groups[0].Items) != 2 || revised.Rule.Groups[0].Items[1].MaxPerCheckIn != 2 {
		t.Fatalf("checkpoint count=%d, want 2", len(revised.Rule.Groups[0].Items))
	}
	var offers []model.ProductOffer
	if err := model.DB.Where("source_product_id = ?", fixture.product.ID).Order("id ASC").Find(&offers).Error; err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 {
		t.Fatalf("offer count=%d, want 2", len(offers))
	}
	for _, offer := range offers {
		if offer.Status != "active" || offer.ProductRevisionID != revised.CurrentRevisionID {
			t.Fatalf("offer was not rebound without suspension: status=%s revision=%d current=%d", offer.Status, offer.ProductRevisionID, revised.CurrentRevisionID)
		}
	}
	if oldRevisionRow.SnapshotJSON == "" {
		t.Fatal("old revision has no snapshot")
	}
	var oldRevisionAfter model.ProductRevision
	if err := model.DB.First(&oldRevisionAfter, oldRevision).Error; err != nil {
		t.Fatal(err)
	}
	if oldRevisionAfter.SnapshotJSON != oldRevisionRow.SnapshotJSON || oldRevisionAfter.Status != "expired" {
		t.Fatalf("historical revision changed: before=%+v after=%+v", oldRevisionRow, oldRevisionAfter)
	}
	if _, err := service.Confirm(fixture.tenant.ID, 11, "admin", preview.PlanID, preview.PlanHash); err != nil {
		t.Fatalf("completed confirm should be idempotent: %v", err)
	}
}

func TestCatalogBatchRejectsCrossTenantAndDistributedTargets(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	foreign := model.Tenant{Name: "Foreign Catalog Tenant", SystemCode: fmt.Sprintf("CATALOG-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&foreign).Error; err != nil {
			return err
		}
		foreignArea := model.ScenicArea{TenantID: foreign.ID, Code: "CATALOG-FOREIGN-AREA", Name: "Foreign Scenic", Status: "active"}
		if err := tx.Create(&foreignArea).Error; err != nil {
			return err
		}
		foreignCheckpoint := model.CheckPoint{Name: "Foreign Gate", TenantID: foreign.ID, ScenicAreaID: foreignArea.ID}
		return tx.Create(&foreignCheckpoint).Error
	}); err != nil {
		t.Fatal(err)
	}
	service := &CatalogBatchChangeService{}
	if _, err := service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		IdempotencyKey: "catalog-batch-cross-tenant", Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductIDs: []uint{fixture.product.ID}, CheckpointIDs: []uint{999999}}},
	}); err == nil {
		t.Fatal("cross-tenant checkpoint was accepted")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		distributed := model.Product{Name: "Imported Listing", TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Type: "online", Status: "online", SourceProductID: fixture.product.ID, SourceTenantID: foreign.ID, RuleID: fixture.product.RuleID, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", GateVoiceCode: "welcome"}
		return tx.Create(&distributed).Error
	}); err != nil {
		t.Fatal(err)
	}
	var distributed model.Product
	if err := model.DB.Where("name = ?", "Imported Listing").First(&distributed).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		IdempotencyKey: "catalog-batch-distributed", Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductIDs: []uint{distributed.ID}, CheckpointIDs: []uint{fixture.extra.ID}}},
	}); err == nil || !strings.Contains(err.Error(), "票种") {
		t.Fatalf("distributed listing error=%v", err)
	}
}

func TestCatalogBatchRejectsStalePlanWithoutPartialChanges(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	second := model.Product{
		Name: "Child Ticket", Price: 59, SettlementPrice: 30, TenantID: fixture.tenant.ID,
		ScenicAreaID: fixture.area.ID, Type: "online", Status: "online", ValidityType: "date",
		StockType: "unlimited", CodeMode: "ticket", RefundType: "free", GateVoiceCode: "welcome",
	}
	secondRule := model.TicketRule{Name: "Child Admission Rule", TenantID: fixture.tenant.ID, ValidityType: "date", Groups: []model.RuleGroup{{GroupName: "Admission", MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: fixture.checkpoint.ID, MaxPerCheckIn: 1}}}}}
	if err := (&ProductService{}).Create(&second, &secondRule); err != nil {
		t.Fatal(err)
	}
	preview, err := (&CatalogBatchChangeService{}).Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		InputText: "给所有票种增加 North Gate 检票点", IdempotencyKey: "catalog-batch-stale-1",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Lines) != 2 {
		t.Fatalf("selected lines=%d, want 2", len(preview.Lines))
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Product{}).Where("id = ?", second.ID).Update("current_revision_id", fixture.product.CurrentRevisionID+999).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&CatalogBatchChangeService{}).Confirm(fixture.tenant.ID, 11, "admin", preview.PlanID, preview.PlanHash); err == nil {
		t.Fatal("stale plan was accepted")
	}
	var firstAfter model.Product
	if err := model.DB.First(&firstAfter, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if firstAfter.CurrentRevisionID != fixture.product.CurrentRevisionID {
		t.Fatalf("partial apply changed first product revision=%d", firstAfter.CurrentRevisionID)
	}
}

func TestCatalogBatchRemoveAndSetLimit(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	service := &CatalogBatchChangeService{}
	preview, err := service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		IdempotencyKey: "catalog-batch-limit-1",
		Operations:     []CatalogRuleOperation{{Kind: CatalogBatchOpSetLimit, ProductIDs: []uint{fixture.product.ID}, CheckpointIDs: []uint{fixture.checkpoint.ID}, MaxPerCheckIn: intPtr(3)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(fixture.tenant.ID, 11, "admin", preview.PlanID, preview.PlanHash); err != nil {
		t.Fatal(err)
	}
	_, err = service.Preview(fixture.tenant.ID, 11, "admin", CatalogBatchChangePreviewRequest{
		IdempotencyKey: "catalog-batch-remove-1",
		Operations:     []CatalogRuleOperation{{Kind: CatalogBatchOpRemoveCheckpoint, ProductIDs: []uint{fixture.product.ID}, CheckpointIDs: []uint{fixture.checkpoint.ID}}},
	})
	if err == nil {
		t.Fatal("removing the only checkpoint should fail validation")
	}
}

func intPtr(value int) *int { return &value }
