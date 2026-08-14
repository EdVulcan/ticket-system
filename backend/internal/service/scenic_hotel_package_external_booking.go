package service

import (
	"errors"
	"strings"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The XHS booking Saga owns remote calls and operation state. These methods
// are the local fulfillment seam: they accept only immutable remote facts and
// keep ticket, entitlement, reservation, inventory and sync state together.
// Keeping the ownership checks here prevents the Saga from becoming a second
// implementation of package fulfillment transitions.

func lockExternalBookingEntitlementTx(tx *gorm.DB, tenantID uint, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	if tx == nil {
		return nil, errors.New("package fulfillment transaction is required")
	}
	if tenantID == 0 {
		return nil, errors.New("package fulfillment tenant is required")
	}
	entitlementNo = strings.TrimSpace(entitlementNo)
	if entitlementNo == "" {
		return nil, errors.New("package entitlement number is required")
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("entitlement_no = ? AND sales_tenant_id = ?", entitlementNo, tenantID).
		First(&entitlement).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func lockExternalBookingEntitlementByIDTx(tx *gorm.DB, tenantID, entitlementID uint) (*model.ScenicHotelPackageEntitlement, error) {
	if tx == nil {
		return nil, errors.New("package fulfillment transaction is required")
	}
	if tenantID == 0 || entitlementID == 0 {
		return nil, errors.New("package fulfillment ownership is required")
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND sales_tenant_id = ?", entitlementID, tenantID).
		First(&entitlement).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func validateExternalBookingFacts(entitlement *model.ScenicHotelPackageEntitlement, externalBookOrderID, platformBookID string, allowEmptyPlatformID bool) error {
	if entitlement == nil {
		return errors.New("package entitlement is required")
	}
	externalBookOrderID = strings.TrimSpace(externalBookOrderID)
	platformBookID = strings.TrimSpace(platformBookID)
	if externalBookOrderID == "" || (!allowEmptyPlatformID && platformBookID == "") {
		return errors.New("external booking identifiers are required")
	}
	if entitlement.ExternalBookOrderID != "" && entitlement.ExternalBookOrderID != externalBookOrderID {
		return errors.New("xiaohongshu external booking order does not match the entitlement")
	}
	if platformBookID != "" && entitlement.PlatformBookID != "" && entitlement.PlatformBookID != platformBookID {
		return errors.New("xiaohongshu platform booking id does not match the entitlement")
	}
	return nil
}

// RecordExternalBookingTx persists the platform result after the remote book
// call. It is idempotent and deliberately does not activate the ticket; the
// platform confirmation and FinalizeExternalBookingTx do that later.
func (lifecycle PackageFulfillmentLifecycle) RecordExternalBookingTx(tx *gorm.DB, tenantID uint, entitlementNo, externalBookOrderID, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	entitlement, err := lockExternalBookingEntitlementTx(tx, tenantID, entitlementNo)
	if err != nil {
		return nil, err
	}
	externalBookOrderID, platformBookID = strings.TrimSpace(externalBookOrderID), strings.TrimSpace(platformBookID)
	if err := validateExternalBookingFacts(entitlement, externalBookOrderID, platformBookID, false); err != nil {
		return nil, err
	}
	if entitlement.Status != "booking_pending" && entitlement.Status != "booked" {
		return nil, errors.New("package entitlement is not awaiting external booking reconciliation")
	}
	updates := map[string]interface{}{
		"external_book_order_id": externalBookOrderID,
		"platform_book_id":       platformBookID,
		"platform_sync_error":    "",
	}
	if entitlement.Status == "booking_pending" {
		updates["platform_sync_status"] = "pending"
	}
	if err := tx.Model(entitlement).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := tx.First(entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return entitlement, nil
}

// FinalizeExternalBookingTx applies the local fulfillment transition only
// after the platform confirmation has succeeded, then records that the two
// states are synchronized.
func (lifecycle PackageFulfillmentLifecycle) FinalizeExternalBookingTx(tx *gorm.DB, tenantID uint, entitlementNo, externalBookOrderID, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	entitlement, err := lockExternalBookingEntitlementTx(tx, tenantID, entitlementNo)
	if err != nil {
		return nil, err
	}
	externalBookOrderID, platformBookID = strings.TrimSpace(externalBookOrderID), strings.TrimSpace(platformBookID)
	if err := validateExternalBookingFacts(entitlement, externalBookOrderID, platformBookID, false); err != nil {
		return nil, err
	}
	if entitlement.Status != "booking_pending" && entitlement.Status != "booked" {
		return nil, errors.New("package entitlement is not awaiting external booking confirmation")
	}
	if entitlement.Status == "booking_pending" {
		if _, err := lifecycle.FinalizeBookingTx(tx, entitlement.EntitlementNo, platformBookID); err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&model.ScenicHotelPackageEntitlement{}).
		Where("id = ? AND sales_tenant_id = ?", entitlement.ID, tenantID).
		Updates(map[string]interface{}{
			"external_book_order_id": externalBookOrderID,
			"platform_book_id":       platformBookID,
			"platform_sync_status":   "synced",
			"platform_sync_error":    "",
		}).Error; err != nil {
		return nil, err
	}
	if err := tx.First(entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return entitlement, nil
}

// RollbackExternalBookingTx releases the local preparation after the remote
// compensation call. A completed rollback is a safe idempotent no-op.
func (lifecycle PackageFulfillmentLifecycle) RollbackExternalBookingTx(tx *gorm.DB, tenantID uint, entitlementNo, externalBookOrderID, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	entitlement, err := lockExternalBookingEntitlementTx(tx, tenantID, entitlementNo)
	if err != nil {
		return nil, err
	}
	if entitlement.Status == "pending_booking" && entitlement.ReservationID == 0 && entitlement.PlatformBookID == "" && entitlement.ExternalBookOrderID == "" {
		return entitlement, nil
	}
	if err := validateExternalBookingFacts(entitlement, strings.TrimSpace(externalBookOrderID), strings.TrimSpace(platformBookID), true); err != nil {
		return nil, err
	}
	if entitlement.Status != "booking_pending" {
		return nil, errors.New("package entitlement is not awaiting external booking compensation")
	}
	return lifecycle.RollbackPreparedBookingTx(tx, entitlement.EntitlementNo)
}

// FinalizeExternalCancellationTx releases the local reservation only after
// the platform has accepted status 3. Status 3 is an appointment revoke, not
// a funds refund.
func (lifecycle PackageFulfillmentLifecycle) FinalizeExternalCancellationTx(tx *gorm.DB, tenantID uint, entitlementNo, externalBookOrderID, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	entitlement, err := lockExternalBookingEntitlementTx(tx, tenantID, entitlementNo)
	if err != nil {
		return nil, err
	}
	if entitlement.Status == "pending_booking" && entitlement.ReservationID == 0 && entitlement.ExternalBookOrderID == "" && entitlement.PlatformBookID == "" && entitlement.PlatformSyncStatus == "synced" {
		return entitlement, nil
	}
	if err := validateExternalBookingFacts(entitlement, strings.TrimSpace(externalBookOrderID), strings.TrimSpace(platformBookID), false); err != nil {
		return nil, err
	}
	if entitlement.Status != "cancel_pending" {
		return nil, errors.New("package entitlement is not awaiting external cancellation")
	}
	return lifecycle.FinalizeCancelTx(tx, entitlement.EntitlementNo)
}

// MarkRefundStatusSyncedTx records completion of the platform status-4
// after-sale notification. It never changes payment, order, ticket or refund
// facts; those are owned by the normal refund workflow.
func (lifecycle PackageFulfillmentLifecycle) MarkRefundStatusSyncedTx(tx *gorm.DB, tenantID uint, entitlementNo, externalBookOrderID, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	entitlement, err := lockExternalBookingEntitlementTx(tx, tenantID, entitlementNo)
	if err != nil {
		return nil, err
	}
	if entitlement.Status != "refunded" {
		return nil, errors.New("package entitlement is not refunded")
	}
	if err := validateExternalBookingFacts(entitlement, strings.TrimSpace(externalBookOrderID), strings.TrimSpace(platformBookID), false); err != nil {
		return nil, err
	}
	if entitlement.PlatformSyncStatus == "synced" {
		return entitlement, nil
	}
	if entitlement.PlatformSyncStatus != "pending" && entitlement.PlatformSyncStatus != "failed" {
		return nil, errors.New("package entitlement has no pending platform after-sale synchronization")
	}
	if err := tx.Model(entitlement).Updates(map[string]interface{}{"platform_sync_status": "synced", "platform_sync_error": ""}).Error; err != nil {
		return nil, err
	}
	return entitlement, nil
}

// MarkExternalBookingRetryPendingTx and MarkExternalBookingSyncFailedTx are
// the only retry bookkeeping writes exposed to the Saga. They retain the
// tenant guard while keeping stage-specific error semantics in fulfillment.
func (lifecycle PackageFulfillmentLifecycle) MarkExternalBookingRetryPendingTx(tx *gorm.DB, tenantID, entitlementID uint) error {
	entitlement, err := lockExternalBookingEntitlementByIDTx(tx, tenantID, entitlementID)
	if err != nil {
		return err
	}
	switch entitlement.Status {
	case "booking_pending", "booked", "cancel_pending", "refunded":
	default:
		return errors.New("package entitlement is not awaiting external synchronization")
	}
	return tx.Model(entitlement).Updates(map[string]interface{}{"platform_sync_status": "pending", "platform_sync_error": ""}).Error
}

func (lifecycle PackageFulfillmentLifecycle) MarkExternalBookingSyncFailedTx(tx *gorm.DB, tenantID, entitlementID uint, message string) error {
	entitlement, err := lockExternalBookingEntitlementByIDTx(tx, tenantID, entitlementID)
	if err != nil {
		return err
	}
	switch entitlement.Status {
	case "booking_pending", "booked", "cancel_pending", "refunded":
	default:
		return errors.New("package entitlement is not awaiting external synchronization")
	}
	return tx.Model(entitlement).Updates(map[string]interface{}{"platform_sync_status": "failed", "platform_sync_error": strings.TrimSpace(message)}).Error
}
