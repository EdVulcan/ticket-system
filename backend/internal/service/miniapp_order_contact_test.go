package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm/logger"
)

const testMiniappContactID = "11010519491231002X"

func updateMiniappContactPolicy(t *testing.T, tenantID, productID uint, required bool) model.Product {
	t.Helper()
	product, err := (&ProductService{}).Get(productID, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	product.ContactRealNameRequired = required
	if err := (&ProductService{}).Update(productID, tenantID, product, &product.Rule); err != nil {
		t.Fatal(err)
	}
	product, err = (&ProductService{}).Get(productID, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	return *product
}

func miniappContactTestInput(mappingID uint, requestID string) MiniappOrderCreateInput {
	return MiniappOrderCreateInput{MappingID: mappingID, Quantity: 3, ClientRequestID: requestID,
		GuestName: "订单联系人", ContactPhone: "13800138000", ContactIDCard: testMiniappContactID}
}

func TestMiniappContactIDCardFormat(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{name: "known checksum", value: testMiniappContactID, valid: true},
		{name: "normalize lowercase", value: " 11010519491231002x ", valid: true},
		{name: "empty"},
		{name: "legacy short format", value: "110105491231002"},
		{name: "wrong checksum", value: "110105194912310021"},
		{name: "non numeric prefix", value: "A1010519491231002X"},
		{name: "zero region prefix", value: contactIDWithCheckDigit("01010519491231002")},
		{name: "invalid date", value: contactIDWithCheckDigit("11010520260230002")},
		{name: "future date", value: contactIDWithCheckDigit("11010520261018002")},
		{name: "outside supported birth years", value: contactIDWithCheckDigit("11010518991231002")},
		{name: "zero sequence", value: contactIDWithCheckDigit("11010519491231000")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMiniappContactIDCard(normalizeMiniappContactIDCard(tc.value), now)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, want %v", err == nil, tc.valid)
			}
			if err != nil && tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Fatal("validation error leaked the submitted identity")
			}
		})
	}
}

func contactIDWithCheckDigit(prefix string) string {
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	sum := 0
	for i := range prefix {
		sum += int(prefix[i]-'0') * weights[i]
	}
	return prefix + string("10X98765432"[sum%11])
}

func TestXiaohongshuContactPolicyRejectsInvalidIdentityWithoutPersisting(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, true)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	for _, identity := range []string{"", "not-an-id", "110105194912310021", contactIDWithCheckDigit("11010520260230002")} {
		input := miniappContactTestInput(fixture.mapping.ID, "invalid-contact")
		input.ContactIDCard = identity
		_, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
		var outcome *MiniappOrderCreateError
		if !errors.As(err, &outcome) || outcome.Code != miniappOrderNotCreatedCode || !strings.Contains(outcome.Message, "身份证") {
			t.Fatalf("invalid identity outcome=%v", err)
		}
	}
	for _, table := range []interface{}{&model.Order{}, &model.XiaohongshuOrderLink{}, &model.XiaohongshuOrderOperation{}} {
		var count int64
		if err := model.DB.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("invalid contact persisted %T rows=%d err=%v", table, count, err)
		}
	}
}

func TestXiaohongshuContactPolicyRejectsCrossTenantAndChannelAccess(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, true)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	for _, tenantID := range []uint{fixture.tenantID, otherTenantID} {
		account := model.ChannelAccount{Code: fmt.Sprintf("other-contact-account-%d", tenantID), Status: "sandbox"}
		if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, fmt.Sprintf("other-contact-app-%d", tenantID), "other-secret"); err != nil {
			t.Fatal(err)
		}
		customer := fixture.customer
		customer.ID = 0
		customer.TenantID, customer.ChannelAccountID = tenantID, account.ID
		customer.SessionTokenHash = hashMiniappValue(account.AppID)
		if err := model.DB.Create(&customer).Error; err != nil {
			t.Fatal(err)
		}
		_, err := service.CreateXiaohongshuOrder(context.Background(), &customer, miniappContactTestInput(fixture.mapping.ID, "foreign-contact"))
		var outcome *MiniappOrderCreateError
		if !errors.As(err, &outcome) || outcome.Code != miniappOrderNotCreatedCode {
			t.Fatalf("foreign mapping accepted: err=%v", err)
		}
	}
	var count int64
	if err := model.DB.Model(&model.Order{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("foreign mapping persisted orders=%d err=%v", count, err)
	}
}

func TestXiaohongshuContactPolicyStoresOneEncryptedSnapshotAndChecksIdempotency(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	firstProduct := updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, true)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	var logs bytes.Buffer
	previousLogger := model.DB.Logger
	model.DB.Logger = logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Info})
	t.Cleanup(func() { model.DB.Logger = previousLogger })
	input := miniappContactTestInput(fixture.mapping.ID, "real-name-multi-ticket")
	result, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Quantity != 3 || !result.ContactRealNameRequired || result.ContactIDCardMasked != "110105********002X" {
		t.Fatal("contact snapshot or multi-ticket quantity was not projected correctly")
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("tenant_id = ? AND order_no = ?", fixture.tenantID, result.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if !order.ContactRealNameRequired || order.ContactIDCardCiphertext == "" || order.VisitorID != "" || len(order.Items) != 1 || len(order.Items[0].Tickets) != 3 {
		t.Fatal("missing encrypted contact snapshot or wrong ticket count")
	}
	if plain, err := utils.DecryptAES(order.ContactIDCardCiphertext); err != nil || plain != testMiniappContactID {
		t.Fatal("contact identity did not round-trip through encryption")
	}
	for _, item := range order.Items {
		if item.VisitorID != "" {
			t.Fatal("order contact leaked into the item visitor identity")
		}
		for _, ticket := range item.Tickets {
			if ticket.VisitorID != "" {
				t.Fatal("order contact leaked into the ticket visitor identity")
			}
		}
	}
	var visitorIdentities int64
	if err := model.DB.Model(&model.OrderVisitor{}).Where("order_id = ? AND identity_no <> ''", order.ID).Count(&visitorIdentities).Error; err != nil || visitorIdentities != 0 {
		t.Fatalf("contact created visitor identities=%d err=%v", visitorIdentities, err)
	}
	var operation model.XiaohongshuOrderOperation
	if err := model.DB.Where("tenant_id = ? AND xiaohongshu_order_link_id = ?", fixture.tenantID, createdOrderLinkID(t, order.OrderNo, fixture.tenantID)).First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
	if err != nil || payload.Fingerprint != miniappOrderIntentFingerprint(input) {
		t.Fatal("encrypted operation is missing its original contact fingerprint")
	}
	for _, value := range []interface{}{order, result, payload.Request, payload} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), testMiniappContactID) || strings.Contains(string(raw), order.ContactIDCardCiphertext) || strings.Contains(string(raw), "contact_id_card\"") {
			t.Fatalf("identity was exposed by %T", value)
		}
	}
	if strings.Contains(logs.String(), testMiniappContactID) {
		t.Fatal("identity leaked into SQL logs")
	}
	storedCiphertext := order.ContactIDCardCiphertext
	normalizedRetry := input
	normalizedRetry.ContactIDCard = " " + strings.ToLower(input.ContactIDCard) + " "
	if replay, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, normalizedRetry); err != nil || replay.OrderNo != result.OrderNo {
		t.Fatalf("normalized retry failed: %v", err)
	}
	changed := input
	changed.ContactIDCard = contactIDWithCheckDigit("11010519491231003")
	_, err = service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, changed)
	var mismatch *MiniappOrderCreateError
	if !errors.As(err, &mismatch) || mismatch.Code != miniappOrderPayloadMismatchCode {
		t.Fatalf("changed contact identity was replayed: %v", err)
	}
	updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, false)
	if replay, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input); err != nil || !replay.ContactRealNameRequired || replay.OrderNo != result.OrderNo {
		t.Fatalf("product edit changed the existing contact policy: %v", err)
	}
	var oldRevision model.ProductRevision
	if err := model.DB.First(&oldRevision, firstProduct.CurrentRevisionID).Error; err != nil || !oldRevision.ContactRealNameRequired {
		t.Fatalf("old revision contact policy changed: %v", err)
	}
	if err := model.DB.First(&order, order.ID).Error; err != nil || order.ContactIDCardCiphertext != storedCiphertext {
		t.Fatal("retry or product edit rewrote the contact snapshot")
	}
	otherCustomer := fixture.customer
	otherCustomer.ID = 0
	otherCustomer.OpenIDHash, otherCustomer.SessionTokenHash = hashMiniappValue("other-contact-open"), hashMiniappValue("other-contact-token")
	if err := model.DB.Create(&otherCustomer).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetXiaohongshuOrder(context.Background(), &otherCustomer, order.OrderNo); err == nil {
		t.Fatal("another customer could access the contact snapshot")
	}
}

func TestXiaohongshuContactPolicyDisabledAndHistoricalRetriesDoNotCollectIdentity(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	input := miniappContactTestInput(fixture.mapping.ID, "anonymous-contact-policy")
	result, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
	if err != nil || result.ContactRealNameRequired || result.ContactIDCardMasked != "" {
		t.Fatalf("disabled policy collected identity: %v", err)
	}
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", fixture.tenantID, result.OrderNo).First(&order).Error; err != nil || order.ContactIDCardCiphertext != "" {
		t.Fatal("disabled policy persisted identity")
	}
	updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, true)
	input.ContactIDCard = ""
	if replay, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input); err != nil || replay.OrderNo != result.OrderNo || replay.ContactRealNameRequired {
		t.Fatalf("historical retry started requiring identity: %v", err)
	}
	input.ClientRequestID = "new-required-contact-policy"
	if _, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input); err == nil {
		t.Fatal("new order bypassed the enabled contact policy")
	}
}

func TestXiaohongshuContactPolicyCannotSatisfyVisitorRealNamePolicy(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	product := updateMiniappContactPolicy(t, fixture.tenantID, fixture.mapping.ProductID, true)
	product.RealNameRequired = true
	if err := (&ProductService{}).Update(product.ID, fixture.tenantID, &product, &product.Rule); err != nil {
		t.Fatal(err)
	}
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	input := miniappContactTestInput(fixture.mapping.ID, "separate-visitor-policy")
	if _, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input); err == nil || !strings.Contains(err.Error(), "visitor") {
		t.Fatalf("contact identity bypassed per-ticket visitors: %v", err)
	}
	input.Quantity = 1
	if _, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input); err == nil || !strings.Contains(err.Error(), "visitor") {
		t.Fatalf("contact identity bypassed single visitor policy: %v", err)
	}
}

func TestXiaohongshuContactPolicyUsesSupplierRevisionInsteadOfListingFlag(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	updateMiniappContactPolicy(t, scenario.supplierID, scenario.sourceProductID, true)
	// Use a genuine supplier-authorized sandbox price within the account's
	// ten-cent ceiling, retaining the normal minimum retail price checks.
	if _, err := (&DistributionService{}).CreateOffer(scenario.supplierID, scenario.distributorID, scenario.sourceProductID, 0.01, 0, "online,ota", nil, nil); err != nil {
		t.Fatal(err)
	}
	var listing model.Product
	if err := model.DB.First(&listing, scenario.listingID).Error; err != nil || listing.ContactRealNameRequired {
		t.Fatalf("expected the sales projection to retain its default policy: %v", err)
	}
	view, err := (&ProductService{}).Get(listing.ID, scenario.distributorID)
	if err != nil || !view.ContactRealNameRequired {
		t.Fatalf("listing detail did not project the supplier policy: %v", err)
	}
	products, _, err := (&ProductService{}).ListFiltered(1, 100, scenario.distributorID, ProductListFilter{})
	if err != nil || len(products) != 1 || !products[0].ContactRealNameRequired {
		t.Fatalf("listing catalog did not project the supplier policy: %v", err)
	}
	view.ContactRealNameRequired = false
	if err := (&ProductService{}).Update(view.ID, scenario.distributorID, view, &view.Rule); err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{Code: "supplier-contact-channel", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(scenario.distributorID, &account, "supplier-contact-app", "supplier-contact-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: listing.ID, ExternalCode: "SUPPLIER-CONTACT", DisplayName: "Distributed contact ticket", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(scenario.distributorID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: scenario.distributorID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "supplier-contact-sku", ProductType: 1, SettleType: 1,
		SyncStatus: "synced", AuditStatus: "approved", OrderPath: "/pages/order/detail",
	}).Error; err != nil {
		t.Fatal(err)
	}
	ciphertext, err := utils.EncryptAES("supplier-contact-openid")
	if err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{
		TenantID: scenario.distributorID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("supplier-contact-openid"), OpenIDCiphertext: ciphertext,
		SessionTokenHash: hashMiniappValue("supplier-contact-token"), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err := (MiniappService{}).ListCatalog(&customer)
	if err != nil || len(catalog.Products) != 1 || !catalog.Products[0].ContactRealNameRequired {
		t.Fatalf("storefront did not read the supplier revision: %v", err)
	}
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	input := miniappContactTestInput(mapping.ID, "supplier-contact-required")
	input.Quantity, input.ContactIDCard = 1, ""
	_, err = service.CreateXiaohongshuOrder(context.Background(), &customer, input)
	var outcome *MiniappOrderCreateError
	if !errors.As(err, &outcome) || outcome.Code != miniappOrderNotCreatedCode || !strings.Contains(outcome.Message, "身份证") {
		t.Fatalf("sales projection disabled the supplier contact policy: %v", err)
	}
	input.ContactIDCard = testMiniappContactID
	result, err := service.CreateXiaohongshuOrder(context.Background(), &customer, input)
	if err != nil || !result.ContactRealNameRequired {
		t.Fatalf("supplier contact order failed: %v", err)
	}
	updateMiniappContactPolicy(t, scenario.supplierID, scenario.sourceProductID, false)
	// Even a forged/stale sales flag must not cause extra identity collection.
	if err := model.DB.Model(&listing).Update("contact_real_name_required", true).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = (MiniappService{}).ListCatalog(&customer)
	if err != nil || len(catalog.Products) != 1 || catalog.Products[0].ContactRealNameRequired {
		t.Fatalf("storefront trusted a sales flag over the updated supplier revision: %v", err)
	}
	input.ClientRequestID = "supplier-contact-disabled"
	result, err = service.CreateXiaohongshuOrder(context.Background(), &customer, input)
	if err != nil || result.ContactRealNameRequired || result.ContactIDCardMasked != "" {
		t.Fatalf("sales flag collected an unnecessary identity: %v", err)
	}
	input.ContactIDCard = "different-unneeded-identity"
	if replay, err := service.CreateXiaohongshuOrder(context.Background(), &customer, input); err != nil || replay.OrderNo != result.OrderNo {
		t.Fatalf("unneeded identity affected the request fingerprint: %v", err)
	}
}

func TestXiaohongshuContactPolicyLegacyRevisionCreationSnapshotsFlag(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND id = ?", fixture.tenantID, fixture.mapping.ProductID).Update("contact_real_name_required", true).Error; err != nil {
		t.Fatal(err)
	}
	var revisions int64
	if err := model.DB.Model(&model.ProductRevision{}).Where("product_id = ?", fixture.mapping.ProductID).Count(&revisions).Error; err != nil || revisions != 0 {
		t.Fatalf("legacy product unexpectedly has revisions=%d err=%v", revisions, err)
	}
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	result, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, miniappContactTestInput(fixture.mapping.ID, "legacy-revision-contact"))
	if err != nil || !result.ContactRealNameRequired {
		t.Fatalf("legacy revision creation lost the contact policy: %v", err)
	}
	var revision model.ProductRevision
	if err := model.DB.Where("product_id = ? AND tenant_id = ?", fixture.mapping.ProductID, fixture.tenantID).First(&revision).Error; err != nil || !revision.ContactRealNameRequired {
		t.Fatalf("legacy revision missing contact snapshot: %v", err)
	}
}

func TestXiaohongshuContactPolicyEditImmediatelyRefreshesCatalog(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	previous := updateMiniappContactPolicy(t, f.tenantID, f.mapping.ProductID, false)
	for _, required := range []bool{false, true, false, true} {
		current := updateMiniappContactPolicy(t, f.tenantID, f.mapping.ProductID, required)
		if current.CurrentRevisionID == previous.CurrentRevisionID {
			t.Fatal("contact policy edit did not create a new supplier revision")
		}
		catalog, err := (MiniappService{}).ListCatalog(&f.customer)
		if err != nil || len(catalog.Products) != 1 || catalog.Products[0].ContactRealNameRequired != required {
			t.Fatalf("catalog lost saved contact policy: required=%t err=%v", required, err)
		}
		raw, err := json.Marshal(catalog.Products[0])
		if err != nil || !strings.Contains(string(raw), fmt.Sprintf(`"contact_real_name_required":%t`, required)) {
			t.Fatal("storefront did not return an explicit contact-policy boolean")
		}
		var soldRevision model.ProductRevision
		if err := model.DB.First(&soldRevision, previous.CurrentRevisionID).Error; err != nil {
			t.Fatal(err)
		}
		if soldRevision.ContactRealNameRequired != previous.ContactRealNameRequired {
			t.Fatal("contact edit changed historical product revision")
		}
		previous = current
	}
}
