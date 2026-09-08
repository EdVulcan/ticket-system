package model

import (
	"strings"
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresSchema114BackfillsOnlyExactVoucherIssuanceReadiness(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{Name: "XHS Issuance Migration", SystemCode: "XHS-ISSUANCE-MIGRATION", SecretKey: "issuance", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: tenant.ID, Code: "XHS-ISSUANCE-AREA", Name: "XHS Issuance Area", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	account := ChannelAccount{TenantID: tenant.ID, Code: "xhs-issuance-migration", Type: "xiaohongshu", AppID: "xhs-issuance-app", Status: "sandbox", Environment: "sandbox"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	customer := MiniappCustomer{
		TenantID: tenant.ID, ChannelAccountID: account.ID, OpenIDHash: strings.Repeat("a", 64),
		OpenIDCiphertext: "sealed-open-id", SessionKeyCiphertext: "sealed-session", SessionTokenHash: strings.Repeat("b", 64),
		SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "XHS Issuance Ticket", ProductKind: "ticket", Type: "online", Status: "online", Price: 1}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{TenantID: tenant.ID, OrderNo: "XHS-ISSUANCE-READY", Channel: "xiaohongshu", ChannelAccountID: account.ID, Status: "paid", Environment: "production"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	item := OrderItem{OrderID: order.ID, ProductID: product.ID, ProductName: product.Name, Quantity: 1, Price: 1, FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	ticket := Ticket{OrderItemID: item.ID, OrderID: order.ID, TenantID: tenant.ID, ScenicAreaID: area.ID, FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID, TicketCode: "XHS-ISSUANCE-TICKET", Status: "unused", Environment: "production"}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	ready := XiaohongshuOrderLink{TenantID: tenant.ID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ClientRequestID: "xhs-issuance-ready", ExternalOrderID: "XHS-ISSUANCE-READY", State: "paid"}
	if err := db.Create(&ready).Error; err != nil {
		t.Fatal(err)
	}
	voucher := XiaohongshuVoucherLink{TenantID: tenant.ID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: ready.ID, TicketID: ticket.ID, VoucherCodeHash: strings.Repeat("c", 64), VoucherCodeCiphertext: "sealed-voucher", Status: 1}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}
	pendingOrder := Order{TenantID: tenant.ID, OrderNo: "XHS-ISSUANCE-PENDING", Channel: "xiaohongshu", ChannelAccountID: account.ID, Status: "paid", Environment: "production"}
	if err := db.Create(&pendingOrder).Error; err != nil {
		t.Fatal(err)
	}
	pending := XiaohongshuOrderLink{TenantID: tenant.ID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: pendingOrder.ID, ClientRequestID: "xhs-issuance-pending", ExternalOrderID: "XHS-ISSUANCE-PENDING", State: "paid"}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`DROP TRIGGER IF EXISTS ownership_guard ON xiaohongshu_order_links`).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"voucher_issuance_status", "voucher_issuance_attempt_count", "voucher_issuance_last_attempt_at", "voucher_issuance_last_error"} {
		if err := db.Exec("ALTER TABLE xiaohongshu_order_links DROP COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= ?", 114).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 113, Name: "xiaohongshu storefront image", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&ready, ready.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&pending, pending.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ready.VoucherIssuanceStatus != "ready" || pending.VoucherIssuanceStatus != "pending" {
		t.Fatalf("legacy issuance readiness ready=%q pending=%q", ready.VoucherIssuanceStatus, pending.VoucherIssuanceStatus)
	}
	if !db.Migrator().HasIndex(&XiaohongshuOrderLink{}, "idx_xhs_order_voucher_issuance_pending") {
		t.Fatal("missing voucher issuance retry index")
	}
	if err := db.Model(&voucher).Update("voucher_code_hash", strings.Repeat("d", 64)).Error; err == nil {
		t.Fatal("voucher binding mutation bypassed PostgreSQL ownership guard")
	}
	foreignTenant := Tenant{Name: "XHS Issuance Foreign", SystemCode: "XHS-ISSUANCE-FOREIGN", SecretKey: "foreign", Status: "active"}
	if err := db.Create(&foreignTenant).Error; err != nil {
		t.Fatal(err)
	}
	foreignAccount := ChannelAccount{TenantID: foreignTenant.ID, Code: "xhs-issuance-foreign", Type: "xiaohongshu", AppID: "xhs-issuance-foreign-app", Status: "sandbox", Environment: "sandbox"}
	if err := db.Create(&foreignAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ready).Update("channel_account_id", foreignAccount.ID).Error; err == nil {
		t.Fatal("cross-account issuance link reassignment bypassed PostgreSQL ownership guard")
	}
}
