package service

import (
	"errors"
	"testing"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestPackageExternalBookingSeamScopesFactsAndStates(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID,
		Nights: 2, RoomsPerPackage: 1, HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 2, Status: "online",
	}); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-external-seam", "active", "production")
	externalBookOrderID, platformBookID := "XHS-SEAM-ORDER", "XHS-SEAM-BOOK"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "seam guest", ContactPhone: "13800138000", ClientRequestID: "seam-request",
			ExternalBookOrderID: externalBookOrderID,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	lifecycle := PackageFulfillmentLifecycle{}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.RecordExternalBookingTx(tx, fixture.tenantID+9999, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant external booking write error=%v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.RecordExternalBookingTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.RecordExternalBookingTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); err != nil {
		t.Fatalf("idempotent external booking write: %v", err)
	}

	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeExternalBookingTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, "WRONG-BOOK-ID")
		return err
	}); err == nil {
		t.Fatal("external platform id replacement was accepted")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeExternalBookingTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "booked" || entitlement.PlatformSyncStatus != "synced" || entitlement.PlatformBookID != platformBookID {
		t.Fatalf("finalized external booking=%+v", entitlement)
	}

	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.PrepareCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeExternalCancellationTx(tx, fixture.tenantID, entitlement.EntitlementNo, "WRONG-ORDER-ID", platformBookID)
		return err
	}); err == nil {
		t.Fatal("external cancellation accepted mismatched order id")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeExternalCancellationTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{
		"status": "refunded", "external_book_order_id": externalBookOrderID, "platform_book_id": platformBookID,
		"platform_sync_status": "pending", "platform_sync_error": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.MarkRefundStatusSyncedTx(tx, fixture.tenantID+9999, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant refund sync error=%v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.MarkRefundStatusSyncedTx(tx, fixture.tenantID, entitlement.EntitlementNo, externalBookOrderID, platformBookID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "refunded" || entitlement.PlatformSyncStatus != "synced" {
		t.Fatalf("refund status sync=%+v", entitlement)
	}
}
