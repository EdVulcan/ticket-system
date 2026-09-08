package model

import (
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresSchema115RefundOperationUpgradeAndRerun(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&XiaohongshuRefundOperation{}) {
		t.Fatal("refund operation table missing")
	}
	// All destructive setup is confined to this process-isolated test database.
	if err := db.Migrator().DropTable(&XiaohongshuRefundOperation{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version >= ?", 115).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 114, Name: "pre-refund snapshot", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, index := range []string{"idx_xhs_refund_external", "idx_xiaohongshu_refund_operations_refund_id"} {
		if !db.Migrator().HasIndex(&XiaohongshuRefundOperation{}, index) {
			t.Fatalf("missing index %s", index)
		}
	}
	invalid := XiaohongshuRefundOperation{TenantID: 999999, ChannelAccountID: 999999, RefundID: 999999, XiaohongshuOrderLinkID: 999999, ExternalAfterSalesOrderID: "invalid", RequestPayloadCiphertext: "sealed", State: "prepared"}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("orphan/cross-tenant refund operation accepted")
	}
}
