package model

import (
	"testing"
	"time"
)

func TestSchema119VoucherGroupUpgradePreservesSingleBindings(t *testing.T) {
	db, _ := upstreamLegacyFixture(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var order Order
	if err := db.Where("order_no = ?", "SUPPLY-1").First(&order).Error; err != nil {
		t.Fatal(err)
	}
	var ticket Ticket
	if err := db.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	account := ChannelAccount{TenantID: order.TenantID, Code: "group-upgrade", Type: "xiaohongshu", Status: "active"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	customer := MiniappCustomer{TenantID: order.TenantID, ChannelAccountID: account.ID, OpenIDHash: "old-open", OpenIDCiphertext: "old-open-cipher", SessionKeyCiphertext: "old-session", SessionTokenHash: "old-token", SessionExpiresAt: time.Now().Add(time.Hour), Status: "active"}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&order).Updates(map[string]interface{}{"channel": "xiaohongshu", "channel_account_id": account.ID}).Error; err != nil {
		t.Fatal(err)
	}
	link := XiaohongshuOrderLink{TenantID: order.TenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ExternalOrderID: "OLD-EXTERNAL", ClientRequestID: "old-request", State: "paid", VoucherIssuanceStatus: "pending"}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	voucher := XiaohongshuVoucherLink{TenantID: order.TenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID, TicketID: ticket.ID, VoucherCodeHash: "old-hash", VoucherCodeCiphertext: "old-cipher", Status: 1}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&link).Update("voucher_issuance_status", "ready").Error; err != nil {
		t.Fatal(err)
	}
	before := legacySupplyBusinessJSON(t, db)
	for _, sql := range []string{"DROP INDEX idx_xiaohongshu_voucher_links_ticket_id", "CREATE UNIQUE INDEX idx_xiaohongshu_voucher_links_ticket_id ON xiaohongshu_voucher_links(ticket_id)", "ALTER TABLE xiaohongshu_voucher_links DROP COLUMN pay_amount_cents", "DELETE FROM schema_migrations WHERE version>=118"} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&SchemaMigration{Version: 118, Name: "prior release", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	var after XiaohongshuVoucherLink
	if err := db.First(&after, voucher.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.TicketID != ticket.ID || after.VoucherCodeCiphertext != voucher.VoucherCodeCiphertext || after.PayAmountCents != nil || legacySupplyBusinessJSON(t, db) != before {
		t.Fatal("upgrade changed old admission or money facts")
	}
	var unique bool
	if err := db.Raw("SELECT indisunique FROM pg_index WHERE indexrelid='idx_xiaohongshu_voucher_links_ticket_id'::regclass").Scan(&unique).Error; err != nil {
		t.Fatal(err)
	}
	if unique {
		t.Fatal("old one-voucher constraint still present")
	}
	if err := db.Raw("SELECT indisunique FROM pg_index WHERE indexrelid='idx_xiaohongshu_voucher_verifications_ticket_id'::regclass").Scan(&unique).Error; err != nil || !unique {
		t.Fatalf("group coordinator uniqueness: %v", err)
	}
	duplicate := voucher
	duplicate.Base = Base{}
	duplicate.VoucherCodeHash = "different-code"
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("ordinary single ticket acquired an extra voucher")
	}
}
