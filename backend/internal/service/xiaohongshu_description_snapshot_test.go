package service

import (
	"context"
	"testing"
	"ticket-backend/internal/model"
)

func TestXiaohongshuDescriptionIsCapturedAtOrderCreation(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	svc, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	const original = "儿童票使用说明\n包含景区入园，不含其他项目。"
	if err := model.DB.Model(&model.XiaohongshuProductConfig{}).Where("channel_product_mapping_id = ?", f.mapping.ID).Update("description", original).Error; err != nil {
		t.Fatal(err)
	}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "description-snapshot", GuestName: "测试", ContactPhone: "13800138000"}
	created, err := svc.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ProductDescription == nil || *created.ProductDescription != original {
		t.Fatalf("missing purchase description: %+v", created.ProductDescription)
	}
	if err := model.DB.Model(&model.XiaohongshuProductConfig{}).Where("channel_product_mapping_id = ?", f.mapping.ID).Update("description", "修改后的介绍").Error; err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil || replayed.OrderNo != created.OrderNo || replayed.ProductDescription == nil || *replayed.ProductDescription != original {
		t.Fatalf("retry replaced snapshot: %+v %v", replayed, err)
	}
	var link model.XiaohongshuOrderLink
	model.DB.Where("order_id IN (SELECT id FROM orders WHERE order_no = ?)", created.OrderNo).First(&link)
	var order model.Order
	model.DB.First(&order, link.OrderID)
	detail, err := (XiaohongshuOrderService{}).orderResult(&link, &order, false)
	if err != nil || detail.ProductDescription == nil || *detail.ProductDescription != original {
		t.Fatalf("detail followed current catalog: %+v %v", detail, err)
	}
}

func TestXiaohongshuLegacyOrderDoesNotBorrowCurrentDescription(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	var link model.XiaohongshuOrderLink
	model.DB.Where("order_id = ?", f.order.ID).First(&link)
	detail, err := (XiaohongshuOrderService{}).orderResult(&link, &f.order, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ProductDescription != nil {
		t.Fatal("legacy order acquired a current description")
	}
}
