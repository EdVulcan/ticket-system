package service

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"ticket-backend/internal/model"
)

func TestCommerceRetailReceiptAfterDispatchPreservesCarrierFacts(t *testing.T) {
	for _, status := range []string{"shipped", "in_transit", "delivered"} {
		t.Run(status, func(t *testing.T) {
			f := newCommerceLogisticsFixture(t, "receipt-owner")
			svc := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return f.now.Add(2 * time.Hour) }}
			if _, err := svc.ConfirmReceipt(f.tenantID, f.orderID); !errors.Is(err, ErrCommerceOrderState) {
				t.Fatalf("unshipped: %v", err)
			}
			shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, ShipmentNo: "RECEIPT-" + status, CarrierCode: "carrier", TrackingNo: "track-" + status, MarkShipped: true})
			if err != nil {
				t.Fatal(err)
			}
			if status != "shipped" {
				if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Status: status, IdempotencyKey: "before-receipt", Description: "Real carrier event", OccurredAt: f.now.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.ConfirmReceipt(f.tenantID+999999, f.orderID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("tenant: %v", err)
			}
			for _, change := range []map[string]interface{}{{"refund_status": "requested"}, {"refund_status": "processing"}, {"refund_status": "none", "payment_status": "pending"}} {
				if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ?", f.orderID).Updates(change).Error; err != nil {
					t.Fatal(err)
				}
				if _, err := svc.ConfirmReceipt(f.tenantID, f.orderID); !errors.Is(err, ErrCommerceOrderState) {
					t.Fatalf("locked %+v: %v", change, err)
				}
			}
			if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ?", f.orderID).Updates(map[string]interface{}{"refund_status": "rejected", "payment_status": "paid"}).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				result, err := svc.ConfirmReceipt(f.tenantID, f.orderID)
				if err != nil || result.FulfillmentStatus != "completed" {
					t.Fatalf("receipt %d: %+v %v", i, result, err)
				}
			}
			timeline, err := f.service.ListShipmentTimeline(f.tenantID, shipment.ID)
			if err != nil {
				t.Fatal(err)
			}
			expectedCount := 1
			if status != "shipped" {
				expectedCount = 2
			}
			if timeline.Shipment.Status != status || len(timeline.Events) != expectedCount || (status != "delivered" && timeline.Shipment.DeliveredAt != nil) {
				t.Fatalf("fabricated carrier facts: %+v", timeline)
			}
			var fulfillment model.RetailFulfillment
			if err := model.DB.Where("order_id = ?", f.orderID).First(&fulfillment).Error; err != nil {
				t.Fatal(err)
			}
			if fulfillment.Status != "completed" || (status != "delivered" && fulfillment.DeliveredAt != nil) {
				t.Fatalf("fulfillment: %+v", fulfillment)
			}
			if status != "delivered" {
				if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Status: "delivered", IdempotencyKey: "late-carrier-delivery", Description: "Real delayed carrier delivery", OccurredAt: f.now.Add(3 * time.Hour)}); err != nil {
					t.Fatalf("late event: %v", err)
				}
				var order model.CommerceOrder
				if err := model.DB.First(&order, f.orderID).Error; err != nil {
					t.Fatal(err)
				}
				if order.FulfillmentStatus != "completed" {
					t.Fatalf("reopened order: %+v", order)
				}
			}
		})
	}
}

func TestCommerceRetailReceiptAvailableActions(t *testing.T) {
	for _, status := range []string{"pending_shipment", "shipped", "in_transit", "delivered", "completed", "cancelled"} {
		for _, refund := range []string{"none", "rejected", "requested", "processing", "completed"} {
			order := &model.CommerceOrder{BusinessType: "retail", PaymentStatus: "paid", RefundStatus: refund, FulfillmentStatus: status, RetailFulfillment: &model.RetailFulfillment{Status: status}}
			want := (status == "shipped" || status == "in_transit" || status == "delivered") && (refund == "none" || refund == "rejected")
			if got := commerceStorefrontAvailableActions(order).CanConfirmReceipt; got != want {
				t.Errorf("%s/%s=%v want %v", status, refund, got, want)
			}
			order.PaymentStatus = "unpaid"
			if commerceStorefrontAvailableActions(order).CanConfirmReceipt {
				t.Errorf("unpaid %s/%s allowed", status, refund)
			}
		}
	}
}

func TestCommerceRetailReceiptRequiresAuthenticatedOwner(t *testing.T) {
	f := newCommerceStorefrontServiceFixture(t)
	owner := storefrontLogin(t, f.service, f.account.AppID, "retail-receipt-owner")
	other := storefrontLogin(t, f.service, f.account.AppID, "retail-receipt-other")
	auth, err := f.service.authenticate(owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	location := model.CommerceFulfillmentLocation{TenantID: f.tenantID, BusinessType: "retail", Name: "Receipt warehouse", LocationType: "warehouse", Status: "active"}
	if err := model.DB.Create(&location).Error; err != nil {
		t.Fatal(err)
	}
	id := createLogisticsOrder(t, f.tenantID, "receipt-owner", location.ID)
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ?", id).Updates(map[string]interface{}{"channel": "wechat_miniapp", "customer_id": auth.CustomerID, "fulfillment_status": "shipped"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.RetailFulfillment{}).Where("order_id = ?", id).Update("status", "shipped").Error; err != nil {
		t.Fatal(err)
	}
	var order model.CommerceOrder
	if err := model.DB.First(&order, id).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfirmReceipt(other.Token, order.OrderNo); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("other customer: %v", err)
	}
	foreignTenantID := createCommerceBoundaryTenant(t, "restaurant")
	foreignDomain := createCommerceBoundaryDomainFixture(t, foreignTenantID, "restaurant")
	foreignAccount, _ := createCommerceStorefrontAccountAndBinding(t, foreignTenantID, foreignDomain.location.ID, "restaurant", "receipt-foreign-account", "wx-receipt-foreign-app")
	foreignLogin := storefrontLogin(t, f.service, foreignAccount.AppID, "retail-receipt-owner")
	if _, err := f.service.ConfirmReceipt(foreignLogin.Token, order.OrderNo); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if result, err := f.service.ConfirmReceipt(owner.Token, order.OrderNo); err != nil || result.FulfillmentStatus != "completed" {
		t.Fatalf("owner: %+v %v", result, err)
	}
}
