package model

import (
	"encoding/json"
	"fmt"
	"testing"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm"
)

func upstreamLegacyFixture(t *testing.T) (*gorm.DB, uint) {
	t.Helper()
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{Name: "Legacy", SystemCode: "SUPPLY-LEGACY", SecretKey: "test", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: tenant.ID, Code: "SUPPLY-AREA", Name: "Legacy", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "Legacy Ticket", Price: 80, Status: "online", Type: "online", GateVoiceCode: "welcome"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	var itemID uint
	for index, status := range []string{"unpaid", "paid", "completed", "refunded", "partial_refunded", "cancelled"} {
		order := Order{TenantID: tenant.ID, OrderNo: fmt.Sprintf("SUPPLY-%d", index), Status: status, TotalAmount: 80, Channel: "online"}
		if err := db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		item := OrderItem{OrderID: order.ID, ProductID: product.ID, ProductName: product.Name, Quantity: 1, Price: 80, FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		itemID = item.ID
		ticket := Ticket{TenantID: tenant.ID, OrderID: order.ID, OrderItemID: item.ID, ScenicAreaID: area.ID, FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID, TicketCode: fmt.Sprintf("UNCHANGED-%d", index), Status: "unused"}
		if err := db.Create(&ticket).Error; err != nil {
			t.Fatal(err)
		}
		if status == "completed" {
			if err := db.Model(&ticket).Updates(map[string]interface{}{"status": "used", "check_in_count": 2}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if status == "paid" || status == "completed" || status == "refunded" {
			paidAt := time.Now().Add(-time.Hour)
			payment := Payment{TenantID: tenant.ID, PaymentNo: fmt.Sprintf("LEGACY-PAY-%d", index), OrderNo: order.OrderNo, Amount: 80, AmountCents: 8000, Method: "cash", Status: "paid", PaidAt: &paidAt}
			if status == "refunded" {
				payment.Status = "refunded"
				payment.RefundedAmount = 80
				payment.RefundedAmountCents = 8000
			}
			if err := db.Create(&payment).Error; err != nil {
				t.Fatal(err)
			}
			if status == "refunded" {
				if err := db.Create(&Refund{TenantID: tenant.ID, RefundNo: "LEGACY-REFUND", IdempotencyKey: "legacy-refund", OrderNo: order.OrderNo, PaymentID: payment.ID, Amount: 80, AmountCents: 8000, Method: "cash", Status: "succeeded"}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&ticket).Update("status", "refunded").Error; err != nil {
					t.Fatal(err)
				}
			}
		}
		if status == "cancelled" {
			if err := db.Delete(&item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Exec(`DROP TRIGGER trg_order_item_supply_snapshot ON order_items`).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []interface{}{&ExternalAdmissionBinding{}, &ExternalAdmissionCredential{}, &OrderItemSupplySnapshot{}, &ProductSupplyConfig{}, &UpstreamProductMapping{}, &UpstreamConnection{}} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= 116").Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 116, Name: "prior release", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	return db, itemID
}

func legacySupplyBusinessJSON(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var data []string
	for _, table := range []string{"orders", "order_items", "tickets", "products", "payments", "refunds", "product_inventories", "check_in_records"} {
		var rows string
		if err := db.Raw("SELECT COALESCE(json_agg(t ORDER BY id)::text,'[]') FROM " + table + " t").Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		data = append(data, rows)
	}
	b, _ := json.Marshal(data)
	return string(b)
}

func TestUpstreamSupply117UpgradePreservesLegacyFactsAndRerun(t *testing.T) {
	db, _ := upstreamLegacyFixture(t)
	before := legacySupplyBusinessJSON(t, db)
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
		if after := legacySupplyBusinessJSON(t, db); after != before {
			t.Fatal("upgrade rewrote existing business facts")
		}
		var snapshots []OrderItemSupplySnapshot
		if err := db.Find(&snapshots).Error; err != nil {
			t.Fatal(err)
		}
		if len(snapshots) != 6 {
			t.Fatalf("snapshots=%d", len(snapshots))
		}
		for _, s := range snapshots {
			if s.Mode != "local" || s.ConnectionID != 0 || s.MappingID != 0 {
				t.Fatalf("legacy reclassified: %+v", s)
			}
		}
	}
	var count int64
	if err := db.Model(&ExternalAdmissionBinding{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("legacy bindings=%d err=%v", count, err)
	}
}

func TestUpstreamSupply117FailedBackfillRollsBackSchema(t *testing.T) {
	db, itemID := upstreamLegacyFixture(t)
	// Deliberately corrupt only this disposable fixture to prove no partial
	// DDL or version marker survives an unresolvable legacy ownership record.
	if err := db.Exec("ALTER TABLE order_items DISABLE TRIGGER USER").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE order_items SET order_id=999999 WHERE id=?", itemID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE order_items ENABLE TRIGGER USER").Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err == nil {
		t.Fatal("orphan backfill succeeded")
	}
	if db.Migrator().HasTable(&OrderItemSupplySnapshot{}) {
		t.Fatal("failed migration left new table")
	}
	var version int
	if err := db.Model(&SchemaMigration{}).Select("MAX(version)").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != 116 {
		t.Fatalf("failed version=%d", version)
	}
}
