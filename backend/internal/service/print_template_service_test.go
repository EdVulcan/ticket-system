package service

import (
	"encoding/json"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestPrintTemplatePublishesImmutableRevisionAndQueuesServerSnapshot(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	definition := DefaultPrintTemplateDefinition()
	definition.Orientation = "landscape"
	definition.Blocks = []model.PrintTemplateBlock{{Kind: "product_name", Align: "center", FontSize: 18, Bold: true}, {Kind: "ticket_code", Align: "left", FontSize: 11}}
	service := &PrintTemplateService{}
	draft, err := service.SaveDraft(tenantID, 11, PrintTemplateSaveRequest{ScenicAreaID: area.ID, ProductID: productID, Name: "窗口票据", PaperWidthMM: 80, Orientation: "landscape", Definition: definition}, "admin")
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if draft.CurrentRevisionID != 0 || draft.DraftRevision == nil {
		t.Fatalf("draft was published unexpectedly: %+v", draft)
	}
	published, err := service.Publish(tenantID, 11, draft.ID, "admin")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.CurrentRevisionID == 0 || published.CurrentDefinition == nil || published.CurrentDefinition.PaperWidthMM != 80 || published.CurrentDefinition.Orientation != "landscape" || published.Orientation != "landscape" {
		t.Fatalf("published template missing current revision: %+v", published)
	}

	posID := createTestPOS(t, tenantID)
	shift, err := (&OperationsService{}).OpenShift(tenantID, posID, 11, 0)
	if err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash", ShiftID: shift.ID, DeviceID: posID, OperatorID: 11}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	job, err := (&OperationsService{}).QueuePrint(tenantID, posID, 11, shift.ID, order.OrderNo, ticket.TicketCode)
	if err != nil {
		t.Fatalf("queue print: %v", err)
	}
	if job.TemplateRevisionID != published.CurrentRevisionID || job.ContentHash == "" || job.PrintDocumentJSON == "" || job.PaperWidthMM != 80 || job.Orientation != "landscape" {
		t.Fatalf("print snapshot missing: %+v", job)
	}
	if !strings.Contains(job.PrintDocumentJSON, ticket.TicketCode) || !strings.Contains(job.PrintDocumentJSON, "Adult Ticket") {
		t.Fatalf("server snapshot did not contain ticket facts: %s", job.PrintDocumentJSON)
	}
	var document model.PrintDocument
	if err := json.Unmarshal([]byte(job.PrintDocumentJSON), &document); err != nil || len(document.Blocks) != 2 || document.Orientation != "landscape" {
		t.Fatalf("invalid print document: err=%v document=%+v", err, document)
	}

	changed := definition
	changed.Blocks = append(changed.Blocks, model.PrintTemplateBlock{Kind: "footer_text", Text: "新版本", Align: "center", FontSize: 9})
	if _, err := service.SaveDraft(tenantID, 11, PrintTemplateSaveRequest{ID: draft.ID, ScenicAreaID: area.ID, ProductID: productID, Name: "窗口票据", PaperWidthMM: 80, Orientation: "landscape", Definition: changed}, "admin"); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Publish(tenantID, 11, draft.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentRevisionID == published.CurrentRevisionID {
		t.Fatal("publish did not create a new revision")
	}
	var unchanged model.PrintJob
	if err := model.DB.First(&unchanged, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.TemplateRevisionID != published.CurrentRevisionID || unchanged.PrintDocumentJSON != job.PrintDocumentJSON || unchanged.ContentHash != job.ContentHash {
		t.Fatal("existing print job was rewritten after template publication")
	}
}

func TestPrintTemplatePreviewAndBindingFailClosedAcrossTenants(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	otherTenant, _ := seedSellableProduct(t, "unlimited", 0)
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	service := &PrintTemplateService{}
	if _, err := service.Preview(tenantID, PrintTemplatePreviewRequest{ScenicAreaID: area.ID, ProductID: productID, Name: "预览", Definition: DefaultPrintTemplateDefinition()}); err != nil {
		t.Fatalf("same tenant preview rejected: %v", err)
	}
	var foreignArea model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", otherTenant).First(&foreignArea).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preview(tenantID, PrintTemplatePreviewRequest{ScenicAreaID: foreignArea.ID, ProductID: productID, Name: "越权", Definition: DefaultPrintTemplateDefinition()}); err == nil {
		t.Fatal("cross-tenant print template preview was accepted")
	}
	if _, err := service.SaveDraft(tenantID, 11, PrintTemplateSaveRequest{ScenicAreaID: area.ID, ProductID: productID, Name: "", Definition: DefaultPrintTemplateDefinition()}, "admin"); err == nil {
		t.Fatal("empty template name was accepted")
	}
	invalid := DefaultPrintTemplateDefinition()
	invalid.Orientation = "diagonal"
	if _, err := service.Preview(tenantID, PrintTemplatePreviewRequest{ScenicAreaID: area.ID, ProductID: productID, Name: "非法方向", Orientation: "diagonal", Definition: invalid}); err == nil {
		t.Fatal("invalid print orientation was accepted")
	}
}

func TestPrintTemplateResolutionPrefersProductOverrideAndRejectsMixedOrderSnapshot(t *testing.T) {
	resetBusinessData(t)
	tenantID, firstProductID := seedSellableProduct(t, "unlimited", 0)
	var firstProduct model.Product
	if err := model.DB.First(&firstProduct, firstProductID).Error; err != nil {
		t.Fatal(err)
	}
	secondProduct := firstProduct
	secondProduct.ID = 0
	secondProduct.Name = "Child Ticket"
	secondProduct.CurrentRevisionID = 0
	secondProduct.CreatedAt = time.Time{}
	secondProduct.UpdatedAt = time.Time{}
	if err := model.DB.Create(&secondProduct).Error; err != nil {
		t.Fatal(err)
	}
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	service := &PrintTemplateService{}
	defaultDefinition := DefaultPrintTemplateDefinition()
	defaultDefinition.Blocks = []model.PrintTemplateBlock{{Kind: "custom_text", Text: "景区默认", Align: "left", FontSize: 10}}
	defaultDraft, err := service.SaveDraft(tenantID, 11, PrintTemplateSaveRequest{ScenicAreaID: area.ID, Name: "景区默认", Definition: defaultDefinition}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(tenantID, 11, defaultDraft.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	productDefinition := DefaultPrintTemplateDefinition()
	productDefinition.Blocks = []model.PrintTemplateBlock{{Kind: "custom_text", Text: "成人专用", Align: "left", FontSize: 10}}
	productDraft, err := service.SaveDraft(tenantID, 11, PrintTemplateSaveRequest{ScenicAreaID: area.ID, ProductID: firstProductID, Name: "成人票模板", Definition: productDefinition}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	productTemplate, err := service.Publish(tenantID, 11, productDraft.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	resolved, resolvedRevision, err := resolvePrintTemplateTx(model.DB, tenantID, area.ID, firstProductID, 0, 11)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != productTemplate.ID || resolvedRevision.ID != productTemplate.CurrentRevisionID {
		t.Fatalf("product template did not override scenic default: template=%+v revision=%+v", resolved, resolvedRevision)
	}

	posID := createTestPOS(t, tenantID)
	shift, err := (&OperationsService{}).OpenShift(tenantID, posID, 11, 0)
	if err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: firstProductID, Quantity: 1}, {ProductID: secondProduct.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash", ShiftID: shift.ID, DeviceID: posID, OperatorID: 11}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&OperationsService{}).QueuePrint(tenantID, posID, 11, shift.ID, order.OrderNo, ""); err == nil || !strings.Contains(err.Error(), "different print templates") {
		t.Fatalf("mixed-template order was not rejected safely: %v", err)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 {
		t.Fatalf("expected two tickets, got %d", len(tickets))
	}
	for _, ticket := range tickets {
		if _, err := (&OperationsService{}).QueuePrint(tenantID, posID, 11, shift.ID, order.OrderNo, ticket.TicketCode); err != nil {
			t.Fatalf("per-ticket snapshot failed for mixed order: %v", err)
		}
	}
}
