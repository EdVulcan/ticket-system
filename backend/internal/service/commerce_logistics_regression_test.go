package service

import (
	"errors"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestCommerceLogisticsManualEventRetryPreservesServerTimestamp(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "manual-event-retry-clock")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID: f.orderID, ShipmentNo: "MANUAL-RETRY-CLOCK", CarrierName: "Carrier", TrackingNo: "MANUAL-RETRY-TRACK", MarkShipped: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := AppendCommerceShipmentEventInput{Status: "delivered", Description: "Recipient signed", IdempotencyKey: "manual-retry-clock"}
	first, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Clock = func() time.Time { return f.now.Add(time.Minute) }
	retry, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, input)
	if err != nil || retry.ID != first.ID || !retry.OccurredAt.Equal(first.OccurredAt) {
		t.Fatalf("retry must return original event and server timestamp: event=%+v err=%v", retry, err)
	}
	for _, change := range []struct {
		name string
		edit func(*AppendCommerceShipmentEventInput)
	}{
		{"explicit timestamp", func(in *AppendCommerceShipmentEventInput) { in.OccurredAt = first.OccurredAt.Add(time.Second) }},
		{"description", func(in *AppendCommerceShipmentEventInput) { in.Description = "Different recipient" }},
		{"payload", func(in *AppendCommerceShipmentEventInput) { in.PayloadJSON = `{"different":true}` }},
	} {
		t.Run(change.name, func(t *testing.T) {
			conflict := input
			change.edit(&conflict)
			if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, conflict); !errors.Is(err, ErrCommerceLogisticsConflict) {
				t.Fatalf("changed request must conflict: %v", err)
			}
		})
	}
	input.OccurredAt = first.OccurredAt
	if retry, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, input); err != nil || retry.ID != first.ID {
		t.Fatalf("explicit original timestamp must replay: event=%+v err=%v", retry, err)
	}
	timeline, err := f.service.ListShipmentTimeline(f.tenantID, shipment.ID)
	if err != nil || len(timeline.Events) != 2 || timeline.Shipment.DeliveredAt == nil || !timeline.Shipment.DeliveredAt.Equal(first.OccurredAt) {
		t.Fatalf("retries must preserve timeline and delivery timestamp: timeline=%+v err=%v", timeline, err)
	}
}

func TestCommerceLogisticsUpdateShippedMetadataRequiresCarrierAndTracking(t *testing.T) {
	for _, status := range []string{"shipped", "in_transit", "delivered"} {
		t.Run(status, func(t *testing.T) {
			f := newCommerceLogisticsFixture(t, "metadata-required-"+status)
			shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
				OrderID: f.orderID, ShipmentNo: "METADATA-" + status, CarrierName: "Carrier", TrackingNo: "TRACK-" + status, MarkShipped: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if status != "shipped" {
				if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{
					Status: status, Description: "Merchant confirms actual shipment progress", IdempotencyKey: "metadata-progress-" + status,
				}); err != nil {
					t.Fatal(err)
				}
			}
			for _, invalid := range []UpdateCommerceShipmentInput{
				{ShipmentNo: shipment.ShipmentNo},
				{ShipmentNo: shipment.ShipmentNo, CarrierName: "Carrier", TrackingNo: "  "},
				{ShipmentNo: shipment.ShipmentNo, CarrierCode: "  ", CarrierName: "  ", TrackingNo: shipment.TrackingNo},
			} {
				if _, err := f.service.UpdateShipment(f.tenantID, shipment.ID, invalid); !errors.Is(err, ErrCommerceLogisticsInvalid) {
					t.Fatalf("missing carrier/tracking must fail: input=%+v err=%v", invalid, err)
				}
			}
			var fulfillment model.RetailFulfillment
			if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).First(&fulfillment).Error; err != nil {
				t.Fatal(err)
			}
			timeline, err := f.service.ListShipmentTimeline(f.tenantID, shipment.ID)
			if err != nil || timeline.Shipment.CarrierName != "Carrier" || timeline.Shipment.TrackingNo != shipment.TrackingNo || fulfillment.Carrier != "Carrier" || fulfillment.TrackingNo != shipment.TrackingNo || fulfillment.Status != status {
				t.Fatalf("rejected updates must preserve shipment and fulfillment: timeline=%+v fulfillment=%+v err=%v", timeline, fulfillment, err)
			}
			valid := UpdateCommerceShipmentInput{ShipmentNo: shipment.ShipmentNo, CarrierCode: "corrected-carrier", TrackingNo: "CORRECTED-" + status, AddressSnapshot: shipment.AddressSnapshot}
			if _, err := f.service.UpdateShipment(f.tenantID+999999, shipment.ID, valid); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("cross-tenant update must fail: %v", err)
			}
			updated, err := f.service.UpdateShipment(f.tenantID, shipment.ID, valid)
			if err != nil || updated.CarrierCode != valid.CarrierCode || updated.TrackingNo != valid.TrackingNo || updated.Status != status {
				t.Fatalf("valid full metadata replacement must remain supported: updated=%+v err=%v", updated, err)
			}
			valid.CarrierCode, valid.CarrierName = "", "Corrected carrier name"
			if _, err := f.service.UpdateShipment(f.tenantID, shipment.ID, valid); err != nil {
				t.Fatalf("carrier name without code must remain supported: %v", err)
			}
			var corrected model.RetailFulfillment
			if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).First(&corrected).Error; err != nil {
				t.Fatal(err)
			}
			if corrected.Carrier != valid.CarrierName || corrected.TrackingNo != valid.TrackingNo || corrected.Status != status {
				t.Fatalf("valid metadata replacement must update only fulfillment metadata: %+v", corrected)
			}
		})
	}
}

func TestCommerceLogisticsUpdateDraftMetadataCanClearCarrierAndTracking(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "metadata-draft-empty")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID: f.orderID, ShipmentNo: "METADATA-DRAFT", CarrierName: "Draft carrier", TrackingNo: "DRAFT-TRACK",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.service.UpdateShipment(f.tenantID, shipment.ID, UpdateCommerceShipmentInput{ShipmentNo: shipment.ShipmentNo})
	if err != nil || updated.CarrierName != "" || updated.TrackingNo != "" || updated.Status != "pending_shipment" {
		t.Fatalf("draft full replacement can clear unconfirmed shipping metadata: updated=%+v err=%v", updated, err)
	}
}
