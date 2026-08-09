package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type teamP0Fixture struct {
	supplier model.Tenant
	travel   model.Tenant
	area     model.ScenicArea
	product  model.Product
	contract model.TravelContract
	device   model.Device
	operator model.User
}

func seedTeamP0Fixture(t *testing.T, creditLimitCents int64) teamP0Fixture {
	t.Helper()
	resetBusinessData(t)
	var fixture teamP0Fixture
	err := model.Write(func(tx *gorm.DB) error {
		fixture.supplier = model.Tenant{Name: "Team Supplier", SystemCode: fmt.Sprintf("TEAM-S-%d", time.Now().UnixNano()), SecretKey: "s", Status: "active"}
		fixture.travel = model.Tenant{Name: "Team Travel", SystemCode: fmt.Sprintf("TEAM-T-%d", time.Now().UnixNano()), SecretKey: "t", Status: "active"}
		if err := tx.Create(&fixture.supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&fixture.travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&[]model.TenantCapability{
			{TenantID: fixture.supplier.ID, Capability: "supplier", Status: "active"},
			{TenantID: fixture.travel.ID, Capability: "travel_agency", Status: "active"},
			{TenantID: fixture.travel.ID, Capability: "distributor", Status: "active"},
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.DistributorRelationship{
			AgentTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID, Status: "active", TravelStatus: "active",
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CapitalAccount{
			OwnerTenantID: fixture.travel.ID, ManagerTenantID: fixture.supplier.ID, Balance: 10000, BalanceCents: 1000000, Status: "active",
		}).Error; err != nil {
			return err
		}
		fixture.area = model.ScenicArea{TenantID: fixture.supplier.ID, Code: fmt.Sprintf("TEAM-AREA-%d", time.Now().UnixNano()), Name: "Team Area", Status: "active"}
		if err := tx.Create(&fixture.area).Error; err != nil {
			return err
		}
		checkpoint := model.CheckPoint{TenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID, Name: "Team Gate"}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return err
		}
		checkpointID := checkpoint.ID
		fixture.device = model.Device{
			TenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID, CheckPointID: &checkpointID,
			Name: "Team Gate Device", SerialNumber: fmt.Sprintf("TEAM-DEVICE-%d", time.Now().UnixNano()), Type: "gate", Status: "online",
		}
		if err := tx.Create(&fixture.device).Error; err != nil {
			return err
		}
		fixture.operator = model.User{TenantID: fixture.supplier.ID, Username: fmt.Sprintf("team-operator-%d", time.Now().UnixNano()), Password: "test", Role: "admin"}
		if err := tx.Create(&fixture.operator).Error; err != nil {
			return err
		}
		rule := model.TicketRule{TenantID: fixture.supplier.ID, Name: "Team Rule", ValidityType: "date"}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		ruleGroup := model.RuleGroup{RuleID: rule.ID, GroupName: "Admission", MaxTotalCheckIn: 1}
		if err := tx.Create(&ruleGroup).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.RuleItem{GroupID: ruleGroup.ID, CheckPointID: checkpoint.ID, MaxPerCheckIn: 1}).Error; err != nil {
			return err
		}
		fixture.product = model.Product{
			TenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID, RuleID: rule.ID,
			Name: "Team Ticket", Type: "online", Status: "online", Price: 100, SettlementPrice: 60,
			IsDistributable: true, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", RefundType: "free",
		}
		if err := tx.Omit("Rule").Create(&fixture.product).Error; err != nil {
			return err
		}
		priceRules, err := json.Marshal([]TeamPriceRule{{ProductID: fixture.product.ID, PriceCents: 6000, MinQuantity: 1}})
		if err != nil {
			return err
		}
		fixture.contract = model.TravelContract{
			TravelTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID,
			ContractNo: fmt.Sprintf("TEAM-CONTRACT-%d", time.Now().UnixNano()), Status: "active",
			CreditLimitCents: creditLimitCents, PriceRulesJSON: string(priceRules),
		}
		return tx.Create(&fixture.contract).Error
	})
	if err != nil {
		t.Fatalf("seed team fixture: %v", err)
	}
	return fixture
}

func createTeamP0Group(t *testing.T, fixture teamP0Fixture, name string, memberCount int) model.TourGroup {
	t.Helper()
	expectedCount := memberCount
	if expectedCount < 1 {
		expectedCount = 1
	}
	group := model.TourGroup{
		Name: name, SupplierTenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID,
		ContractID: fixture.contract.ID, VisitDate: time.Now().Add(24 * time.Hour), ExpectedCount: expectedCount,
	}
	service := &TeamService{}
	if err := service.CreateGroup(fixture.travel.ID, &group); err != nil {
		t.Fatalf("create team group: %v", err)
	}
	members := make([]model.TourGroupMember, memberCount)
	for i := range members {
		members[i].Name = fmt.Sprintf("Visitor %d", i+1)
	}
	if len(members) > 0 {
		if _, err := service.ReplaceMembers(fixture.travel.ID, group.ID, members); err != nil {
			t.Fatalf("replace team members: %v", err)
		}
	}
	return group
}

func TestTeamZeroCreditDoesNotAuthorizeUnfundedContractOrder(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 0)
	group := createTeamP0Group(t, fixture, "Zero Credit Team", 1)

	if _, err := (&TeamService{}).CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err == nil {
		t.Fatal("zero contract credit was treated as unlimited and created an unfunded paid order")
	}
}

func TestTeamContractOrderRequiresMinimumGroupSize(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	priceRules := fmt.Sprintf(`[{"product_id":%d,"price_cents":6000,"min_quantity":2}]`, fixture.product.ID)
	if err := model.DB.Model(&model.TravelContract{}).Where("id = ?", fixture.contract.ID).
		Update("price_rules_json", priceRules).Error; err != nil {
		t.Fatal(err)
	}
	group := createTeamP0Group(t, fixture, "Below Minimum Team", 1)

	if _, err := (&TeamService{}).CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err == nil {
		t.Fatal("contract order accepted a team below the configured minimum group size")
	}
}

func TestLegacyTeamMaximumDoesNotBecomeMinimum(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	legacy := fixture.contract
	legacy.PriceRulesJSON = fmt.Sprintf(`[{"product_id":%d,"price_cents":6000,"max_quantity":100}]`, fixture.product.ID)
	order := model.Order{Items: []model.OrderItem{{
		FulfillmentProductID: fixture.product.ID,
		SettlementPrice:      60,
		Quantity:             1,
	}}}

	if err := validateTeamOrderAgainstContract(&legacy, &order); err != nil {
		t.Fatalf("legacy maximum quantity was incorrectly treated as a minimum: %v", err)
	}
}

func TestTeamContractDoesNotCreateDistributionOffer(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	var secondTravel model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		secondTravel = model.Tenant{Name: "Second Travel", SystemCode: fmt.Sprintf("TEAM-T2-%d", time.Now().UnixNano()), SecretKey: "t2", Status: "active"}
		if err := tx.Create(&secondTravel).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: secondTravel.ID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
			return err
		}
		return tx.Create(&model.DistributorRelationship{AgentTenantID: secondTravel.ID, SupplierTenantID: fixture.supplier.ID, TravelStatus: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	_, err := (&TeamService{}).CreateContract(fixture.supplier.ID, fixture.operator.ID, TravelContractInput{
		TravelTenantID: secondTravel.ID, ContractNo: fmt.Sprintf("NO-OFFER-%d", time.Now().UnixNano()), Status: "active",
		CreditLimitCents: 100000, PriceRules: []TeamPriceRule{{ProductID: fixture.product.ID, PriceCents: 5500, MinQuantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := model.DB.Model(&model.ProductOffer{}).
		Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ?", fixture.supplier.ID, secondTravel.ID, fixture.product.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("team contract created %d ordinary distribution offers", count)
	}
}

func TestTeamContractDoesNotOverwriteDistributionOffer(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	if err := model.Write(func(tx *gorm.DB) error {
		revision, err := ensureProductRevisionTx(tx, &fixture.product)
		if err != nil {
			return err
		}
		return tx.Create(&model.ProductOffer{
			SupplierTenantID: fixture.supplier.ID, DistributorTenantID: fixture.travel.ID,
			SourceProductID: fixture.product.ID, ProductRevisionID: revision.ID, FulfillmentScenicAreaID: fixture.area.ID,
			SettlementPrice: 72, MinimumRetailPriceCents: 8000, AllowedChannels: "ota", Status: "active",
		}).Error
	}); err != nil {
		t.Fatal(err)
	}

	_, err := (&TeamService{}).UpdateContract(fixture.supplier.ID, fixture.contract.ID, fixture.operator.ID, TravelContractInput{
		TravelTenantID: fixture.travel.ID, ContractNo: fixture.contract.ContractNo, Status: "active",
		CreditLimitCents: 100000, PriceRules: []TeamPriceRule{{ProductID: fixture.product.ID, PriceCents: 5500, MinQuantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var offer model.ProductOffer
	if err := model.DB.Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ?", fixture.supplier.ID, fixture.travel.ID, fixture.product.ID).First(&offer).Error; err != nil {
		t.Fatal(err)
	}
	if moneyCents(offer.SettlementPrice) != 7200 || offer.MinimumRetailPriceCents != 8000 || offer.AllowedChannels != "ota" {
		t.Fatalf("team contract overwrote ordinary distribution offer: %+v", offer)
	}
}

func TestTeamAddMembersRejectsOrSanitizesSystemFieldsAndConfirmedGroups(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "Roster Guard Team", 0)
	now := time.Now()
	count, err := (&TeamService{}).AddMembers(fixture.travel.ID, group.ID, []model.TourGroupMember{{
		Name: "Forged Visitor", Status: "entered", TicketCode: "FORGED-TICKET", EnteredAt: &now, EntryBatchNo: "FORGED-BATCH",
	}})
	if err == nil {
		if count != 1 {
			t.Fatalf("added count=%d, want 1", count)
		}
		var stored model.TourGroupMember
		if err := model.DB.Where("group_id = ?", group.ID).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "planned" || stored.TicketCode != "" || stored.EnteredAt != nil || stored.EntryBatchNo != "" {
			t.Fatalf("client-controlled admission fields were persisted: %+v", stored)
		}
	}

	if err := model.DB.Model(&model.TourGroup{}).Where("id = ?", group.ID).Update("status", "confirmed").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&TeamService{}).AddMembers(fixture.travel.ID, group.ID, []model.TourGroupMember{{Name: "Late Visitor"}}); err == nil {
		t.Fatal("confirmed team accepted the unaudited add-members path")
	}
}

func seedAttachOrderScenario(t *testing.T) (teamP0Fixture, model.Order) {
	t.Helper()
	fixture := seedTeamP0Fixture(t, 1000000)
	if err := model.Write(func(tx *gorm.DB) error {
		revision, err := ensureProductRevisionTx(tx, &fixture.product)
		if err != nil {
			return err
		}
		offer := model.ProductOffer{
			SupplierTenantID: fixture.supplier.ID, DistributorTenantID: fixture.travel.ID,
			SourceProductID: fixture.product.ID, ProductRevisionID: revision.ID, FulfillmentScenicAreaID: fixture.area.ID,
			SettlementPrice: 60, MinimumRetailPriceCents: 6000, AllowedChannels: "window", Status: "active",
		}
		if err := tx.Create(&offer).Error; err != nil {
			return err
		}
		listing := model.Product{
			TenantID: fixture.travel.ID, ScenicAreaID: fixture.area.ID, SourceProductID: fixture.product.ID, ProductOfferID: offer.ID,
			Name: "Team Source Listing", Type: "online", Status: "online", Price: 80, SettlementPrice: 60,
			ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", RefundType: "free",
		}
		return tx.Omit("Rule").Create(&listing).Error
	}); err != nil {
		t.Fatal(err)
	}
	var listing model.Product
	if err := model.DB.Where("tenant_id = ? AND source_product_id = ?", fixture.travel.ID, fixture.product.ID).First(&listing).Error; err != nil {
		t.Fatal(err)
	}
	return fixture, createAttachableTeamOrder(t, fixture, listing, 2)
}

func createAttachableTeamOrder(t *testing.T, fixture teamP0Fixture, listing model.Product, quantity int) model.Order {
	t.Helper()
	useDate := time.Now().Add(24 * time.Hour)
	order := model.Order{TenantID: fixture.travel.ID, Channel: "window", Items: []model.OrderItem{{ProductID: listing.ID, Quantity: quantity, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create attachable order: %v", err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.travel.ID); err != nil {
		t.Fatalf("pay attachable order: %v", err)
	}
	return order
}

func TestTeamAttachOrderRequiresDraftUnboundGroup(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Already Confirmed", 1)
	if err := model.DB.Model(&model.TourGroup{}).Where("id = ?", group.ID).Update("status", "confirmed").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TeamService{}).AttachOrder(fixture.travel.ID, group.ID, order.ID); err == nil {
		t.Fatal("non-draft group accepted an order attachment")
	}
}

func TestTeamAttachOrderCannotReuseTicketAssignedToAnotherGroup(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	first := createTeamP0Group(t, fixture, "First Team", 1)
	second := createTeamP0Group(t, fixture, "Second Team", 1)
	service := &TeamService{}
	if err := service.AttachOrder(fixture.travel.ID, first.ID, order.ID); err != nil {
		t.Fatalf("attach first team: %v", err)
	}
	if err := service.AttachOrder(fixture.travel.ID, second.ID, order.ID); err == nil {
		t.Fatal("the same ticket entitlement was assigned to members in two active teams")
	}
}

func TestTeamAttachPaidOrderDoesNotConsumeContractCreditAgain(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Already Paid Team", 2)
	if err := (&TeamService{}).AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
		t.Fatalf("attach paid order: %v", err)
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CreditUsedCents != 0 {
		t.Fatalf("paid order consumed team credit again: %d", stored.CreditUsedCents)
	}
	const supplierSettlementCents = int64(2 * 6000)
	if stored.DepositCents != supplierSettlementCents || stored.ContractAmountCents != supplierSettlementCents {
		t.Fatalf("paid supplier funding=%d contract amount=%d, want %d", stored.DepositCents, stored.ContractAmountCents, supplierSettlementCents)
	}
}

func TestTeamCreateRejectsUnavailableSupplier(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.supplier.ID, "supplier").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	group := model.TourGroup{
		Name: "Unavailable supplier", SupplierTenantID: fixture.supplier.ID,
		ScenicAreaID: fixture.area.ID, ContractID: fixture.contract.ID,
		VisitDate: time.Now().Add(24 * time.Hour), ExpectedCount: 1,
	}
	if err := (&TeamService{}).CreateGroup(fixture.travel.ID, &group); err == nil {
		t.Fatal("team creation accepted an unavailable supplier")
	}
}

func TestTeamContractActionsRejectUnavailableSupplier(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Supplier suspended after planning", 1)
	if err := model.DB.Model(&model.TenantCapability{}).
		Where("tenant_id = ? AND capability = ?", fixture.supplier.ID, "supplier").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	service := &TeamService{}
	if err := service.AttachOrder(fixture.travel.ID, group.ID, order.ID); err == nil {
		t.Fatal("paid order was attached after supplier capability was suspended")
	}
	if _, err := service.CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err == nil {
		t.Fatal("contract order was created after supplier capability was suspended")
	}
}

func TestTeamContractActionsRejectSuspendedTravelPartnership(t *testing.T) {
	fixture, order := seedAttachOrderScenario(t)
	group := createTeamP0Group(t, fixture, "Partnership suspended after planning", 1)
	if err := model.DB.Model(&model.DistributorRelationship{}).
		Where("agent_tenant_id = ? AND supplier_tenant_id = ?", fixture.travel.ID, fixture.supplier.ID).
		Update("travel_status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	service := &TeamService{}
	if err := service.AttachOrder(fixture.travel.ID, group.ID, order.ID); err == nil {
		t.Fatal("paid order was attached after the travel partnership was suspended")
	}
	if _, err := service.CreateContractOrder(fixture.travel.ID, group.ID, fixture.operator.ID, TeamOrderInput{ProductID: fixture.product.ID}); err == nil {
		t.Fatal("contract order was created after the travel partnership was suspended")
	}
}

func TestConcurrentTeamEntryAdmitsMemberExactlyOnceAcrossDifferentKeys(t *testing.T) {
	fixture, firstOrder := seedAttachOrderScenario(t)
	var listing model.Product
	if err := model.DB.Where("tenant_id = ? AND source_product_id = ?", fixture.travel.ID, fixture.product.ID).First(&listing).Error; err != nil {
		t.Fatal(err)
	}
	const (
		rounds  = 4
		workers = 24
	)
	for round := 0; round < rounds; round++ {
		order := firstOrder
		if round > 0 {
			order = createAttachableTeamOrder(t, fixture, listing, 2)
		}
		group := createTeamP0Group(t, fixture, fmt.Sprintf("Concurrent Entry Team %d", round), 1)
		if err := (&TeamService{}).AttachOrder(fixture.travel.ID, group.ID, order.ID); err != nil {
			t.Fatal(err)
		}
		members, err := (&TeamService{}).ListMembers(fixture.travel.ID, group.ID)
		if err != nil || len(members) != 1 {
			t.Fatalf("round %d members=%+v err=%v", round, members, err)
		}
		start := make(chan struct{})
		results := make(chan error, workers)
		var wait sync.WaitGroup
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				_, callErr := (&TeamService{}).EnterBatch(
					fixture.supplier.ID, group.ID, fixture.device.ID, fixture.operator.ID,
					[]uint{members[0].ID}, fmt.Sprintf("concurrent-entry-%d-%d", round, index),
				)
				results <- callErr
			}(i)
		}
		close(start)
		wait.Wait()
		close(results)
		successes := 0
		for result := range results {
			if result == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("round %d concurrent admission successes=%d, want exactly 1", round, successes)
		}
		var checkIns int64
		if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_code = ? AND result = ?", members[0].TicketCode, "success").Count(&checkIns).Error; err != nil {
			t.Fatal(err)
		}
		if checkIns != 1 {
			t.Fatalf("round %d successful check-in facts=%d, want 1", round, checkIns)
		}
	}
}
