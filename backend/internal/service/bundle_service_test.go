package service

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type bundleSupplierSeed struct {
	supplierID, sourceProductID, listingID, checkpointID, deviceID uint
}

func addBundleSupplier(t *testing.T, distributorID uint, name string, settlement float64, stock int) bundleSupplierSeed {
	t.Helper()
	var seeded bundleSupplierSeed
	err := model.Write(func(tx *gorm.DB) error {
		supplier := model.Tenant{Name: name, SystemCode: fmt.Sprintf("BUNDLE-SUP-%d", time.Now().UnixNano()), SecretKey: "secret", Status: "active"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: supplier.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: supplier.ID, Code: fmt.Sprintf("BUNDLE-AREA-%d", supplier.ID), Name: name + " Park", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		checkpoint := model.CheckPoint{TenantID: supplier.ID, ScenicAreaID: area.ID, Name: name + " Gate"}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return err
		}
		checkpointID := checkpoint.ID
		device := model.Device{TenantID: supplier.ID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, Name: name + " Device", SerialNumber: fmt.Sprintf("BUNDLE-DEV-%d", time.Now().UnixNano()), Type: "gate", Status: "online", AuthKeyHash: hashDeviceKey("test-device-key")}
		if err := tx.Create(&device).Error; err != nil {
			return err
		}
		rule := model.TicketRule{TenantID: supplier.ID, Name: name + " Rule", ValidityType: "date"}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		group := model.RuleGroup{RuleID: rule.ID, GroupName: "Admission", MaxTotalCheckIn: 1}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: checkpoint.ID, MaxPerCheckIn: 1}).Error; err != nil {
			return err
		}
		product := model.Product{
			TenantID: supplier.ID, ScenicAreaID: area.ID, RuleID: rule.ID, Name: name + " Ticket",
			Price: settlement + 30, SettlementPrice: settlement, Type: "online", Status: "online", IsDistributable: true,
			ValidityType: "date", StockType: "total", DailyStock: stock, CodeMode: "ticket",
		}
		if err := tx.Omit("Rule").Create(&product).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.DistributorRelationship{AgentTenantID: distributorID, SupplierTenantID: supplier.ID, Status: "active"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CapitalAccount{OwnerTenantID: distributorID, ManagerTenantID: supplier.ID, Balance: 1000, Status: "active"}).Error; err != nil {
			return err
		}
		seeded = bundleSupplierSeed{supplierID: supplier.ID, sourceProductID: product.ID, checkpointID: checkpoint.ID, deviceID: device.ID}
		return nil
	})
	if err != nil {
		t.Fatalf("seed bundle supplier: %v", err)
	}
	if _, err := (&DistributionService{}).CreateOffer(seeded.supplierID, distributorID, seeded.sourceProductID, settlement, 0, "window,online", nil, nil); err != nil {
		t.Fatalf("create bundle offer: %v", err)
	}
	if err := (&DistributionService{}).ImportProduct(distributorID, seeded.sourceProductID, name+" Listing", settlement+30, "online"); err != nil {
		t.Fatalf("import bundle listing: %v", err)
	}
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND source_product_id = ?", distributorID, seeded.sourceProductID).Pluck("id", &seeded.listingID).Error; err != nil {
		t.Fatalf("load bundle listing: %v", err)
	}
	return seeded
}

func seedCrossSupplierBundle(t *testing.T, secondStock int) (uint, bundleSupplierSeed, bundleSupplierSeed, *BundleView) {
	t.Helper()
	firstScenario := seedDistributionScenario(t)
	first := bundleSupplierSeed{
		supplierID: firstScenario.supplierID, sourceProductID: firstScenario.sourceProductID, listingID: firstScenario.listingID,
		checkpointID: firstScenario.supplierCheckpointID, deviceID: firstScenario.supplierDeviceID,
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", firstScenario.sourceProductID, firstScenario.supplierID).Update("daily_stock", 3).Error; err != nil {
			return err
		}
		return tx.Model(&model.CapitalAccount{}).
			Where("owner_tenant_id = ? AND manager_tenant_id = ?", firstScenario.distributorID, firstScenario.supplierID).
			Updates(map[string]interface{}{"balance": 1000, "balance_cents": int64(100000)}).Error
	}); err != nil {
		t.Fatalf("fund first bundle supplier account: %v", err)
	}
	second := addBundleSupplier(t, firstScenario.distributorID, "Supplier B", 40, secondStock)
	bundle, err := (&BundleService{}).Create(firstScenario.distributorID, 1, BundleUpsertInput{
		Name: "Two Parks Pass", Type: "online", RetailPriceCents: 15000,
		Components: []BundleComponentInput{
			{SellerProductID: first.listingID, Quantity: 1, RetailAllocationCents: 8000},
			{SellerProductID: second.listingID, Quantity: 1, RetailAllocationCents: 7000},
		},
	})
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	if err := (&BundleService{}).SetStatus(firstScenario.distributorID, bundle.ID, 1, "online", ""); err != nil {
		t.Fatalf("enable bundle: %v", err)
	}
	return firstScenario.distributorID, first, second, bundle
}

func TestCrossSupplierBundleOrderCreatesIndependentFulfillmentsAndRefundsAllocation(t *testing.T) {
	resetBusinessData(t)
	distributorID, first, second, bundle := seedCrossSupplierBundle(t, 2)
	order := model.Order{TenantID: distributorID, Channel: "online", ContactName: "Bundle Visitor", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create bundle order: %v", err)
	}
	if moneyCents(order.TotalAmount) != 15000 || len(order.Items) != 2 {
		t.Fatalf("bundle total/items=%d/%d, want 15000/2", moneyCents(order.TotalAmount), len(order.Items))
	}
	for _, item := range order.Items {
		if item.BundleProductID != bundle.ID || item.BundleVersionID != bundle.CurrentVersionID || item.BundleComponentID == 0 || item.BundleName != bundle.Name {
			t.Fatalf("missing bundle snapshot on item: %+v", item)
		}
	}
	var fulfillments []model.FulfillmentOrder
	if err := model.DB.Where("sales_order_id = ?", order.ID).Order("supplier_tenant_id").Find(&fulfillments).Error; err != nil {
		t.Fatal(err)
	}
	if len(fulfillments) != 2 || fulfillments[0].SupplierTenantID == fulfillments[1].SupplierTenantID {
		t.Fatalf("fulfillments=%+v, want two suppliers", fulfillments)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].FulfillmentTenantID == tickets[1].FulfillmentTenantID {
		t.Fatalf("tickets=%+v, want independent supplier tickets", tickets)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, distributorID); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range tickets {
		checkpoint, device := first.checkpointID, first.deviceID
		if ticket.FulfillmentTenantID == second.supplierID {
			checkpoint, device = second.checkpointID, second.deviceID
		}
		if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint, device, ticket.FulfillmentTenantID); err != nil {
			t.Fatalf("supplier %d rejected its bundle ticket: %v", ticket.FulfillmentTenantID, err)
		}
	}

	// A fresh order is used for refund because used tickets must fail closed.
	refundable := model.Order{TenantID: distributorID, Channel: "online", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&refundable); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: refundable.OrderNo, Method: "cash"}
	if err := (&PaymentService{}).CreatePayment(distributorID, &payment); err != nil {
		t.Fatal(err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ? AND fulfillment_tenant_id = ?", refundable.ID, first.supplierID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_item_id = ?", item.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&RefundService{}).CreateCashRefund(distributorID, refundable.OrderNo, "bundle-component-refund", 80, []string{ticket.TicketCode}, "visitor changed plans"); err != nil {
		t.Fatalf("refund bundle component allocation: %v", err)
	}
	var stored model.Order
	if err := model.DB.First(&stored, refundable.ID).Error; err != nil || stored.Status != "partial_refunded" {
		t.Fatalf("order after component refund=%+v err=%v", stored, err)
	}
}

func TestCrossSupplierBundleOrderRollsBackWhenOneSupplierHasNoStock(t *testing.T) {
	resetBusinessData(t)
	distributorID, first, second, bundle := seedCrossSupplierBundle(t, 0)
	order := model.Order{TenantID: distributorID, Channel: "online", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	err := (&OrderService{}).Create(&order)
	if err == nil || !strings.Contains(err.Error(), "insufficient stock") {
		t.Fatalf("bundle stock failure=%v", err)
	}
	var firstProduct model.Product
	if err := model.DB.First(&firstProduct, first.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if firstProduct.DailyStock != 3 {
		t.Fatalf("first supplier stock=%d after rollback, want 3", firstProduct.DailyStock)
	}
	for _, supplierID := range []uint{first.supplierID, second.supplierID} {
		var account model.CapitalAccount
		if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", distributorID, supplierID).First(&account).Error; err != nil {
			t.Fatal(err)
		}
		if moneyCents(account.Balance) != 10000 && moneyCents(account.Balance) != 100000 {
			// The first legacy seed account starts at 100, the second at 1000.
			t.Fatalf("supplier account changed after rollback: %+v", account)
		}
	}
	var count int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ?", distributorID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rolled back bundle order count=%d err=%v", count, err)
	}
}

func TestBundleStopsNewSalesWhenSupplierTermsChangeButSoldRightsRemain(t *testing.T) {
	resetBusinessData(t)
	distributorID, first, _, bundle := seedCrossSupplierBundle(t, 2)
	order := model.Order{TenantID: distributorID, Channel: "online", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, distributorID); err != nil {
		t.Fatal(err)
	}
	var offer model.ProductOffer
	if err := model.DB.Where("supplier_tenant_id = ? AND distributor_tenant_id = ?", first.supplierID, distributorID).First(&offer).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Model(&offer).Update("settlement_price", 61).Error }); err != nil {
		t.Fatal(err)
	}
	secondOrder := model.Order{TenantID: distributorID, Channel: "online", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&secondOrder); err == nil || !strings.Contains(err.Error(), "revise the bundle") {
		t.Fatalf("changed supplier terms did not stop bundle: %v", err)
	}
	var soldTicket model.Ticket
	if err := model.DB.Where("order_id = ? AND fulfillment_tenant_id = ?", order.ID, first.supplierID).First(&soldTicket).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(soldTicket.TicketCode, first.checkpointID, first.deviceID, first.supplierID); err != nil {
		t.Fatalf("previously sold bundle right became invalid: %v", err)
	}
}

func TestBundleTenantIsolation(t *testing.T) {
	resetBusinessData(t)
	distributorID, _, _, bundle := seedCrossSupplierBundle(t, 2)
	if _, err := (&BundleService{}).Get(distributorID+999, bundle.ID); err == nil {
		t.Fatal("cross-tenant bundle was readable")
	}
}

func TestBundleRevisionPreservesOldOrderSnapshot(t *testing.T) {
	resetBusinessData(t)
	distributorID, first, second, bundle := seedCrossSupplierBundle(t, 3)
	oldVersionID := bundle.CurrentVersionID
	order := model.Order{TenantID: distributorID, Channel: "online", Items: []model.OrderItem{{BundleProductID: bundle.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	revised, err := (&BundleService{}).Revise(distributorID, bundle.ID, 1, BundleUpsertInput{
		Name: "Two Parks Pass Plus", Type: "online", RetailPriceCents: 16000,
		Components: []BundleComponentInput{
			{SellerProductID: first.listingID, Quantity: 1, RetailAllocationCents: 9000},
			{SellerProductID: second.listingID, Quantity: 1, RetailAllocationCents: 7000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if revised.CurrentVersionID == oldVersionID || revised.Status != "offline" || revised.Version != 2 {
		t.Fatalf("revised bundle=%+v", revised)
	}
	for _, item := range order.Items {
		if item.BundleVersionID != oldVersionID {
			t.Fatalf("old order bundle version changed: %+v", item)
		}
	}
}

func TestPOSHoldPreservesAndRevalidatesBundleSelection(t *testing.T) {
	resetBusinessData(t)
	distributorID, _, _, bundle := seedCrossSupplierBundle(t, 2)
	var components []model.BundleComponent
	if err := model.DB.Where("bundle_version_id = ?", bundle.CurrentVersionID).Find(&components).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		productIDs := make([]uint, 0, len(components))
		for i := range components {
			productIDs = append(productIDs, components[i].SellerProductID)
		}
		if err := tx.Model(&model.Product{}).Where("tenant_id = ? AND id IN ?", distributorID, productIDs).Update("type", "offline").Error; err != nil {
			return err
		}
		return tx.Model(&model.BundleProduct{}).Where("id = ? AND seller_tenant_id = ?", bundle.ID, distributorID).Update("type", "offline").Error
	}); err != nil {
		t.Fatal(err)
	}
	var device model.Device
	err := model.Write(func(tx *gorm.DB) error {
		area := model.ScenicArea{TenantID: distributorID, Code: "POS-BUNDLE", Name: "Bundle POS", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		device = model.Device{TenantID: distributorID, ScenicAreaID: area.ID, Name: "Bundle POS", SerialNumber: "BUNDLE-POS", Type: "pos", Status: "online"}
		return tx.Create(&device).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	operations := &OperationsService{}
	shift, err := operations.OpenShift(distributorID, device.ID, 77, 0)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := operations.CreatePOSHold(distributorID, device.ID, 77, shift.ID, []model.POSHoldLine{{BundleProductID: bundle.ID, Quantity: 1}}, "Visitor", "", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if hold.TotalCents != 15000 || len(hold.Items) != 1 || hold.Items[0].BundleProductID != bundle.ID || hold.Items[0].ProductID != 0 {
		t.Fatalf("bundle hold=%+v", hold)
	}
	resumed, err := operations.ResumePOSHold(distributorID, hold.ID, 77)
	if err != nil || resumed.Status != "resumed" || resumed.Items[0].BundleProductID != bundle.ID {
		t.Fatalf("resumed bundle hold=%+v err=%v", resumed, err)
	}
}
