package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// MigrationAuditIssue is an immutable record of a legacy row that needs
// review before a migration can be treated as safe. Quarantining is explicit:
// the normal order, ticket, and fulfillment queries never include these rows.
type MigrationAuditIssue struct {
	Base
	RunID        string `gorm:"size:40;index;not null" json:"run_id"`
	Severity     string `gorm:"size:20;index;not null" json:"severity"`
	Code         string `gorm:"size:80;index;not null" json:"code"`
	EntityType   string `gorm:"size:80;index;not null" json:"entity_type"`
	EntityID     uint   `gorm:"index" json:"entity_id"`
	TenantID     uint   `gorm:"index" json:"tenant_id"`
	ScenicAreaID uint   `gorm:"index" json:"scenic_area_id"`
	Detail       string `gorm:"type:text;not null" json:"detail"`
	Status       string `gorm:"size:20;index;not null;default:'open'" json:"status"`
}

type MigrationAuditReport struct {
	RunID         string                `json:"run_id"`
	GeneratedAt   time.Time             `json:"generated_at"`
	SchemaVersion int                   `json:"schema_version"`
	SafeToMigrate bool                  `json:"safe_to_migrate"`
	Issues        []MigrationAuditIssue `json:"issues"`
}

func AuditLegacyMigration(db *gorm.DB) (*MigrationAuditReport, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	report := &MigrationAuditReport{
		RunID:         fmt.Sprintf("MIG-AUDIT-%d", time.Now().UnixNano()),
		GeneratedAt:   time.Now().UTC(),
		SafeToMigrate: true,
		Issues:        make([]MigrationAuditIssue, 0),
	}
	if hasTable(db, "schema_migrations") {
		_ = db.Table("schema_migrations").Select("COALESCE(MAX(version), 0)").Scan(&report.SchemaVersion).Error
	}
	add := func(severity, code, entity string, id, tenantID, scenicID uint, detail string) {
		if severity == "error" {
			report.SafeToMigrate = false
		}
		report.Issues = append(report.Issues, MigrationAuditIssue{
			RunID: report.RunID, Severity: severity, Code: code, EntityType: entity,
			EntityID: id, TenantID: tenantID, ScenicAreaID: scenicID, Detail: detail, Status: "open",
		})
	}

	// A row with no physical fulfillment area cannot be safely migrated by
	// guessing. It must be quarantined or assigned by an operator first.
	for _, check := range []struct {
		table, entity, selectExpr, where, detail string
	}{
		{"products", "product", "id, tenant_id, scenic_area_id", "scenic_area_id = 0", "product has no scenic area"},
		{"check_points", "checkpoint", "id, tenant_id, scenic_area_id", "scenic_area_id = 0", "checkpoint has no scenic area"},
		{"devices", "device", "id, tenant_id, scenic_area_id", "scenic_area_id = 0", "device has no scenic area"},
		{"order_items", "order_item", "id, 0 AS tenant_id, fulfillment_scenic_area_id AS scenic_area_id", "fulfillment_scenic_area_id = 0", "order item has no fulfillment scenic area"},
		{"tickets", "ticket", "id, tenant_id, scenic_area_id", "fulfillment_scenic_area_id = 0", "ticket has no fulfillment scenic area"},
		{"fulfillment_orders", "fulfillment_order", "id, supplier_tenant_id AS tenant_id, scenic_area_id", "scenic_area_id = 0", "fulfillment order has no scenic area"},
		{"ticket_entitlements", "ticket_entitlement", "id, supplier_tenant_id AS tenant_id, scenic_area_id", "scenic_area_id = 0", "ticket entitlement has no scenic area"},
	} {
		if !hasTable(db, check.table) {
			continue
		}
		var rows []struct {
			ID           uint
			TenantID     uint `gorm:"column:tenant_id"`
			ScenicAreaID uint `gorm:"column:scenic_area_id"`
		}
		query := db.Table(check.table).Select(check.selectExpr).Where(check.where).Limit(10000)
		if err := query.Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("audit %s: %w", check.table, err)
		}
		for _, row := range rows {
			add("error", "zero_scenic_area", check.entity, row.ID, row.TenantID, row.ScenicAreaID, check.detail)
		}
	}

	// Legacy offers with no trustworthy supplier price or revision are never
	// promoted into an active authorization.
	if hasTable(db, "product_offers") {
		var rows []struct {
			ID                      uint
			SupplierTenantID        uint    `gorm:"column:supplier_tenant_id"`
			FulfillmentScenicAreaID uint    `gorm:"column:fulfillment_scenic_area_id"`
			SettlementPrice         float64 `gorm:"column:settlement_price"`
			ProductRevisionID       uint    `gorm:"column:product_revision_id"`
		}
		if err := db.Table("product_offers").Select("id, supplier_tenant_id, fulfillment_scenic_area_id, settlement_price, product_revision_id").Where("settlement_price <= 0 OR fulfillment_scenic_area_id = 0 OR product_revision_id = 0").Limit(10000).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("audit product offers: %w", err)
		}
		for _, row := range rows {
			add("error", "untrusted_offer", "product_offer", row.ID, row.SupplierTenantID, row.FulfillmentScenicAreaID, fmt.Sprintf("settlement_price=%.2f product_revision_id=%d", row.SettlementPrice, row.ProductRevisionID))
		}
	}

	// Orphans are reported individually so an operator can repair only the
	// affected facts instead of taking the whole tenant offline.
	for _, check := range []struct {
		table, entity, selectExpr, where, detail string
	}{
		{"order_items", "order_item", "id, 0 AS tenant_id", "NOT EXISTS (SELECT 1 FROM orders WHERE orders.id = order_items.order_id)", "order item references a missing order"},
		{"tickets", "ticket", "id, tenant_id", "NOT EXISTS (SELECT 1 FROM order_items WHERE order_items.id = tickets.order_item_id)", "ticket references a missing order item"},
		{"fulfillment_orders", "fulfillment_order", "id, supplier_tenant_id AS tenant_id", "NOT EXISTS (SELECT 1 FROM orders WHERE orders.id = fulfillment_orders.sales_order_id)", "fulfillment order references a missing sales order"},
	} {
		if !hasTable(db, check.table) {
			continue
		}
		var rows []struct {
			ID       uint
			TenantID uint `gorm:"column:tenant_id"`
		}
		if err := db.Table(check.table).Select(check.selectExpr).Where(check.where).Limit(10000).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("audit %s orphans: %w", check.table, err)
		}
		for _, row := range rows {
			add("error", "orphan_reference", check.entity, row.ID, row.TenantID, 0, check.detail)
		}
	}

	if hasTable(db, "channel_accounts") {
		var duplicates []struct {
			Code  string
			Count int64 `gorm:"column:count"`
		}
		if err := db.Table("channel_accounts").Select("code, COUNT(*) AS count").Group("code").Having("COUNT(*) > 1").Scan(&duplicates).Error; err != nil {
			return nil, fmt.Errorf("audit channel codes: %w", err)
		}
		for _, duplicate := range duplicates {
			add("error", "duplicate_channel_code", "channel_account", 0, 0, 0, "duplicate channel code: "+duplicate.Code)
		}
	}
	return report, nil
}

func PersistMigrationAudit(db *gorm.DB, report *MigrationAuditReport) error {
	if db == nil || report == nil {
		return fmt.Errorf("database and report are required")
	}
	if err := db.AutoMigrate(&MigrationAuditIssue{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range report.Issues {
			row := report.Issues[i]
			row.Base = Base{}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func MigrationAuditJSON(report *MigrationAuditReport) ([]byte, error) {
	if report == nil {
		return nil, fmt.Errorf("report is required")
	}
	return json.MarshalIndent(report, "", "  ")
}

func hasTable(db *gorm.DB, table string) bool {
	if strings.TrimSpace(table) == "" {
		return false
	}
	var count int64
	return db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error == nil && count > 0
}
