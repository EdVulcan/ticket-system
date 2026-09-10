package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestSupplySnapshotSurvivesReschedule(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "whole"
		if partial {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
			resetBusinessData(t)
			tenantID, productID := seedSellableProduct(t, "daily", 5)
			originalDate := startOfDay(time.Now().AddDate(0, 0, 1))
			targetDate := originalDate.AddDate(0, 0, 1)
			quantity := 1
			if partial {
				quantity = 2
			}
			order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: quantity, UseDate: &originalDate}}}
			if err := (&OrderService{}).Create(&order); err != nil {
				t.Fatal(err)
			}
			if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
				t.Fatal(err)
			}
			var before model.OrderItemSupplySnapshot
			if err := model.DB.Where("order_id = ?", order.ID).First(&before).Error; err != nil {
				t.Fatal(err)
			}
			var ticket model.Ticket
			if err := model.DB.Where("order_id = ?", order.ID).Order("id").First(&ticket).Error; err != nil {
				t.Fatal(err)
			}
			req := model.AfterSaleRequest{TenantID: tenantID, OrderNo: order.OrderNo, Type: "reschedule", IdempotencyKey: "supply-reschedule", TargetDate: &targetDate, OperatorID: 1}
			svc := &AfterSaleService{}
			if err := svc.Create(&req, []string{ticket.TicketCode}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Approve(tenantID, req.ID, 2, "approved"); err != nil {
				t.Fatal(err)
			}
			result, err := svc.Execute(tenantID, req.ID, 2)
			if err != nil || result.Status != "completed" {
				t.Fatalf("reschedule=%+v err=%v", result, err)
			}
			var after model.OrderItemSupplySnapshot
			if err := model.DB.First(&after, before.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("reschedule rewrote original supply identity")
			}
			var snapshots []model.OrderItemSupplySnapshot
			if err := model.DB.Where("order_id = ?", order.ID).Find(&snapshots).Error; err != nil {
				t.Fatal(err)
			}
			if len(snapshots) != quantity {
				t.Fatalf("snapshot count=%d want=%d", len(snapshots), quantity)
			}
			for _, snapshot := range snapshots {
				if snapshot.Mode != "local" || snapshot.ProductID != before.ProductID || snapshot.ProductRevisionID != before.ProductRevisionID || snapshot.FulfillmentTenantID != before.FulfillmentTenantID || snapshot.ScenicAreaID != before.ScenicAreaID {
					t.Fatalf("split lost supply identity: %+v", snapshot)
				}
			}
		})
	}
}
