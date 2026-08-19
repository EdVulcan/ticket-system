package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	printTemplateStatusActive   = "active"
	printTemplateStatusDisabled = "disabled"
	printRevisionDraft          = "draft"
	printRevisionPublished      = "published"
	printRevisionRetired        = "retired"
)

var supportedPrintBlockKinds = map[string]struct{}{
	"scenic_name": {}, "logo": {}, "product_name": {}, "use_date": {},
	"validity": {}, "visitor_name": {}, "visitor_phone_suffix": {},
	"order_no": {}, "ticket_code": {}, "qr_code": {}, "barcode": {},
	"ticket_sequence": {}, "checkpoint_summary": {}, "price": {},
	"custom_text": {}, "footer_text": {},
}

// DefaultPrintTemplateDefinition is deliberately small and deterministic. It
// is materialized as a normal published revision the first time a scenic area
// prints, so tenants can edit it without a special code path.
func DefaultPrintTemplateDefinition() model.PrintTemplateDefinition {
	return model.PrintTemplateDefinition{
		SchemaVersion: 1,
		PaperWidthMM:  58,
		Blocks: []model.PrintTemplateBlock{
			{Kind: "scenic_name", Align: "center", FontSize: 18, Bold: true, Spacing: 1},
			{Kind: "product_name", Align: "center", FontSize: 15, Bold: true, Spacing: 1},
			{Kind: "use_date", Align: "left", FontSize: 11, Spacing: 1},
			{Kind: "order_no", Align: "left", FontSize: 10},
			{Kind: "ticket_code", Align: "left", FontSize: 11, Bold: true},
			{Kind: "qr_code", Align: "center", FontSize: 12, Spacing: 1},
			{Kind: "ticket_sequence", Align: "center", FontSize: 10},
			{Kind: "checkpoint_summary", Align: "left", FontSize: 10},
			{Kind: "footer_text", Align: "center", FontSize: 9, Text: "请妥善保管票据，入园时出示二维码"},
		},
	}
}

type PrintTemplateSaveRequest struct {
	ID                uint                          `json:"id"`
	ScenicAreaID      uint                          `json:"scenic_area_id"`
	ProductID         uint                          `json:"product_id"`
	ProductRevisionID uint                          `json:"product_revision_id"`
	Name              string                        `json:"name"`
	PaperWidthMM      int                           `json:"paper_width_mm"`
	PrinterProfile    string                        `json:"printer_profile"`
	Definition        model.PrintTemplateDefinition `json:"definition"`
}

type PrintTemplatePreviewRequest struct {
	ScenicAreaID      uint                          `json:"scenic_area_id"`
	ProductID         uint                          `json:"product_id"`
	ProductRevisionID uint                          `json:"product_revision_id"`
	Name              string                        `json:"name"`
	PaperWidthMM      int                           `json:"paper_width_mm"`
	Definition        model.PrintTemplateDefinition `json:"definition"`
}

type PrintTemplateView struct {
	model.PrintTemplate
	ProductName       string                         `json:"product_name,omitempty"`
	ProductRevision   int                            `json:"product_revision,omitempty"`
	CurrentRevision   *model.PrintTemplateRevision   `json:"current_revision,omitempty"`
	DraftRevision     *model.PrintTemplateRevision   `json:"draft_revision,omitempty"`
	CurrentDefinition *model.PrintTemplateDefinition `json:"definition,omitempty"`
	DraftDefinition   *model.PrintTemplateDefinition `json:"draft_definition,omitempty"`
}

type PrintTemplateRevisionView struct {
	model.PrintTemplateRevision
	Definition *model.PrintTemplateDefinition `json:"definition,omitempty"`
}

type PrintTemplateService struct{}

func normalizePrintTemplateDefinition(def model.PrintTemplateDefinition, paperWidth int) (model.PrintTemplateDefinition, error) {
	if def.SchemaVersion == 0 {
		def.SchemaVersion = 1
	}
	if def.SchemaVersion != 1 {
		return model.PrintTemplateDefinition{}, errors.New("unsupported print template schema version")
	}
	if paperWidth == 0 {
		paperWidth = def.PaperWidthMM
	}
	if paperWidth != 58 && paperWidth != 80 {
		return model.PrintTemplateDefinition{}, errors.New("paper width must be 58 or 80 mm")
	}
	if len(def.Blocks) == 0 || len(def.Blocks) > 64 {
		return model.PrintTemplateDefinition{}, errors.New("print template must contain 1 to 64 blocks")
	}
	def.PaperWidthMM = paperWidth
	for index := range def.Blocks {
		block := &def.Blocks[index]
		if _, ok := supportedPrintBlockKinds[block.Kind]; !ok {
			return model.PrintTemplateDefinition{}, fmt.Errorf("unsupported print block %q", block.Kind)
		}
		if block.Align == "" {
			block.Align = "left"
		}
		if block.Align != "left" && block.Align != "center" && block.Align != "right" {
			return model.PrintTemplateDefinition{}, fmt.Errorf("block %d has invalid alignment", index+1)
		}
		if block.FontSize == 0 {
			block.FontSize = 12
		}
		if block.FontSize < 8 || block.FontSize > 32 {
			return model.PrintTemplateDefinition{}, fmt.Errorf("block %d font size must be 8 to 32", index+1)
		}
		if block.Spacing < 0 || block.Spacing > 8 {
			return model.PrintTemplateDefinition{}, fmt.Errorf("block %d spacing must be 0 to 8", index+1)
		}
		block.Text = strings.TrimSpace(block.Text)
		if (block.Kind == "custom_text" || block.Kind == "footer_text") && block.Text == "" {
			return model.PrintTemplateDefinition{}, fmt.Errorf("block %d requires text", index+1)
		}
		if utf8.RuneCountInString(block.Text) > 500 {
			return model.PrintTemplateDefinition{}, fmt.Errorf("block %d text is too long", index+1)
		}
	}
	return def, nil
}

func printDefinitionHash(def model.PrintTemplateDefinition) (string, string, error) {
	encoded, err := json.Marshal(def)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(encoded)
	return string(encoded), hex.EncodeToString(digest[:]), nil
}

func decodePrintDefinition(raw string) (*model.PrintTemplateDefinition, error) {
	var definition model.PrintTemplateDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err != nil {
		return nil, err
	}
	normalized, err := normalizePrintTemplateDefinition(definition, definition.PaperWidthMM)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func (s *PrintTemplateService) List(tenantID, scenicAreaID, productID uint) ([]PrintTemplateView, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	query := model.DB.Where("tenant_id = ?", tenantID)
	if scenicAreaID != 0 {
		query = query.Where("scenic_area_id = ?", scenicAreaID)
	}
	if productID != 0 {
		query = query.Where("product_id = ?", productID)
	}
	var templates []model.PrintTemplate
	if err := query.Order("scenic_area_id ASC, product_id ASC, product_revision_id ASC, id ASC").Find(&templates).Error; err != nil {
		return nil, err
	}
	views := make([]PrintTemplateView, 0, len(templates))
	for index := range templates {
		view, err := s.viewForTemplate(model.DB, &templates[index])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (s *PrintTemplateService) Get(tenantID, id uint) (*PrintTemplateView, error) {
	var template model.PrintTemplate
	if err := model.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&template).Error; err != nil {
		return nil, err
	}
	return s.viewForTemplate(model.DB, &template)
}

func (s *PrintTemplateService) ListRevisions(tenantID, templateID uint) ([]PrintTemplateRevisionView, error) {
	var template model.PrintTemplate
	if err := model.DB.Where("id = ? AND tenant_id = ?", templateID, tenantID).First(&template).Error; err != nil {
		return nil, err
	}
	var revisions []model.PrintTemplateRevision
	if err := model.DB.Where("template_id = ? AND tenant_id = ?", templateID, tenantID).Order("version DESC, id DESC").Find(&revisions).Error; err != nil {
		return nil, err
	}
	rows := make([]PrintTemplateRevisionView, 0, len(revisions))
	for index := range revisions {
		definition, err := decodePrintDefinition(revisions[index].DefinitionJSON)
		if err != nil {
			return nil, err
		}
		rows = append(rows, PrintTemplateRevisionView{PrintTemplateRevision: revisions[index], Definition: definition})
	}
	return rows, nil
}

func (s *PrintTemplateService) Preview(tenantID uint, req PrintTemplatePreviewRequest) (*struct {
	Definition  model.PrintTemplateDefinition `json:"definition"`
	Document    model.PrintDocument           `json:"document"`
	ContentHash string                        `json:"content_hash"`
}, error) {
	if tenantID == 0 || req.ScenicAreaID == 0 {
		return nil, errors.New("scenic area is required")
	}
	definition, err := normalizePrintTemplateDefinition(req.Definition, req.PaperWidthMM)
	if err != nil {
		return nil, err
	}
	if err := validatePrintBinding(model.DB, tenantID, req.ScenicAreaID, req.ProductID, req.ProductRevisionID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "样例模板"
	}
	document := renderSamplePrintDocument(name, definition)
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return &struct {
		Definition  model.PrintTemplateDefinition `json:"definition"`
		Document    model.PrintDocument           `json:"document"`
		ContentHash string                        `json:"content_hash"`
	}{Definition: definition, Document: document, ContentHash: hex.EncodeToString(digest[:])}, nil
}

func (s *PrintTemplateService) SaveDraft(tenantID, actorID uint, req PrintTemplateSaveRequest, actorRole ...string) (*PrintTemplateView, error) {
	if tenantID == 0 || actorID == 0 || req.ScenicAreaID == 0 {
		return nil, errors.New("tenant, operator and scenic area are required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return nil, errors.New("template name is required and must be at most 100 characters")
	}
	definition, err := normalizePrintTemplateDefinition(req.Definition, req.PaperWidthMM)
	if err != nil {
		return nil, err
	}
	definitionJSON, definitionHash, err := printDefinitionHash(definition)
	if err != nil {
		return nil, err
	}
	var template model.PrintTemplate
	err = model.Write(func(tx *gorm.DB) error {
		if err := validatePrintBinding(tx, tenantID, req.ScenicAreaID, req.ProductID, req.ProductRevisionID); err != nil {
			return err
		}
		paperWidth := definition.PaperWidthMM
		profile := strings.TrimSpace(req.PrinterProfile)
		if profile == "" {
			profile = "escpos"
		}
		if req.ID == 0 {
			template = model.PrintTemplate{TenantID: tenantID, ScenicAreaID: req.ScenicAreaID, ProductID: req.ProductID, ProductRevisionID: req.ProductRevisionID, Name: name, Status: printTemplateStatusActive, PaperWidthMM: paperWidth, PrinterProfile: profile}
			if err := tx.Create(&template).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", req.ID, tenantID).First(&template).Error; err != nil {
				return err
			}
			if template.ScenicAreaID != req.ScenicAreaID || template.ProductID != req.ProductID || template.ProductRevisionID != req.ProductRevisionID {
				if template.CurrentRevisionID != 0 {
					return errors.New("published template binding cannot be changed; create a new template")
				}
				template.ScenicAreaID, template.ProductID, template.ProductRevisionID = req.ScenicAreaID, req.ProductID, req.ProductRevisionID
			}
			template.Name, template.PaperWidthMM, template.PrinterProfile = name, paperWidth, profile
			if err := tx.Model(&template).Updates(map[string]interface{}{"name": name, "scenic_area_id": template.ScenicAreaID, "product_id": template.ProductID, "product_revision_id": template.ProductRevisionID, "paper_width_mm": paperWidth, "printer_profile": profile}).Error; err != nil {
				return err
			}
		}
		var draft model.PrintTemplateRevision
		err := tx.Where("template_id = ? AND tenant_id = ? AND status = ?", template.ID, tenantID, printRevisionDraft).Order("version DESC").First(&draft).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var latest model.PrintTemplateRevision
			version := 1
			if err := tx.Where("template_id = ? AND tenant_id = ?", template.ID, tenantID).Order("version DESC").First(&latest).Error; err == nil {
				version = latest.Version + 1
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			draft = model.PrintTemplateRevision{TenantID: tenantID, ScenicAreaID: template.ScenicAreaID, TemplateID: template.ID, Version: version, Status: printRevisionDraft, DefinitionJSON: definitionJSON, DefinitionHash: definitionHash, CreatedBy: actorID}
			if err := tx.Create(&draft).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if err := tx.Model(&draft).Updates(map[string]interface{}{"definition_json": definitionJSON, "definition_hash": definitionHash, "created_by": actorID}).Error; err != nil {
				return err
			}
		}
		role := "tenant"
		if len(actorRole) > 0 && strings.TrimSpace(actorRole[0]) != "" {
			role = actorRole[0]
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "print_template.draft", "print_template", template.ID, "保存打印模板草稿", "", fmt.Sprintf(`{"revision":%d,"hash":%q}`, draft.ID, definitionHash))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(tenantID, template.ID)
}

func (s *PrintTemplateService) Publish(tenantID, actorID, templateID uint, actorRole ...string) (*PrintTemplateView, error) {
	var template model.PrintTemplate
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", templateID, tenantID).First(&template).Error; err != nil {
			return err
		}
		var draft model.PrintTemplateRevision
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("template_id = ? AND tenant_id = ? AND status = ?", templateID, tenantID, printRevisionDraft).Order("version DESC").First(&draft).Error; err != nil {
			return errors.New("no draft revision to publish")
		}
		if _, err := decodePrintDefinition(draft.DefinitionJSON); err != nil {
			return fmt.Errorf("draft definition is invalid: %w", err)
		}
		now := time.Now()
		if err := tx.Model(&model.PrintTemplateRevision{}).Where("template_id = ? AND tenant_id = ? AND status = ?", templateID, tenantID, printRevisionPublished).Updates(map[string]interface{}{"status": printRevisionRetired}).Error; err != nil {
			return err
		}
		if err := tx.Model(&draft).Updates(map[string]interface{}{"status": printRevisionPublished, "published_by": actorID, "published_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&template).Updates(map[string]interface{}{"current_revision_id": draft.ID, "status": printTemplateStatusActive}).Error; err != nil {
			return err
		}
		role := "tenant"
		if len(actorRole) > 0 && strings.TrimSpace(actorRole[0]) != "" {
			role = actorRole[0]
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "print_template.publish", "print_template", template.ID, "发布打印模板版本", "", fmt.Sprintf(`{"revision":%d,"version":%d}`, draft.ID, draft.Version))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(tenantID, templateID)
}

func (s *PrintTemplateService) SetStatus(tenantID, actorID, templateID uint, status string, actorRole ...string) (*PrintTemplateView, error) {
	status = strings.TrimSpace(status)
	if status != printTemplateStatusActive && status != printTemplateStatusDisabled {
		return nil, errors.New("unsupported print template status")
	}
	err := model.Write(func(tx *gorm.DB) error {
		var template model.PrintTemplate
		if err := tx.Where("id = ? AND tenant_id = ?", templateID, tenantID).First(&template).Error; err != nil {
			return err
		}
		if template.CurrentRevisionID == 0 && status == printTemplateStatusActive {
			return errors.New("template must be published before activation")
		}
		if err := tx.Model(&template).Update("status", status).Error; err != nil {
			return err
		}
		role := "tenant"
		if len(actorRole) > 0 && strings.TrimSpace(actorRole[0]) != "" {
			role = actorRole[0]
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "print_template.status", "print_template", template.ID, "变更打印模板状态", "", fmt.Sprintf(`{"status":%q}`, status))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(tenantID, templateID)
}

func (s *PrintTemplateService) viewForTemplate(db *gorm.DB, template *model.PrintTemplate) (*PrintTemplateView, error) {
	view := &PrintTemplateView{PrintTemplate: *template}
	if template.ProductID != 0 {
		var product model.Product
		if err := db.Where("id = ? AND tenant_id = ?", template.ProductID, template.TenantID).First(&product).Error; err == nil {
			view.ProductName = product.Name
		}
	}
	var revisions []model.PrintTemplateRevision
	if err := db.Where("template_id = ? AND tenant_id = ?", template.ID, template.TenantID).Order("version DESC").Find(&revisions).Error; err != nil {
		return nil, err
	}
	for index := range revisions {
		definition, err := decodePrintDefinition(revisions[index].DefinitionJSON)
		if err != nil {
			return nil, err
		}
		if revisions[index].ID == template.CurrentRevisionID || revisions[index].Status == printRevisionPublished {
			copyRevision := revisions[index]
			view.CurrentRevision = &copyRevision
			view.CurrentDefinition = definition
			if view.ProductRevision == 0 {
				view.ProductRevision = revisions[index].Version
			}
		}
		if revisions[index].Status == printRevisionDraft && view.DraftRevision == nil {
			copyRevision := revisions[index]
			view.DraftRevision = &copyRevision
			view.DraftDefinition = definition
		}
	}
	return view, nil
}

func validatePrintBinding(db *gorm.DB, tenantID, scenicAreaID, productID, productRevisionID uint) error {
	var area model.ScenicArea
	if scenicAreaID == 0 || db.Where("id = ? AND tenant_id = ? AND status != ?", scenicAreaID, tenantID, "closed").First(&area).Error != nil {
		return errors.New("scenic area is not owned by tenant")
	}
	if productID == 0 {
		if productRevisionID != 0 {
			return errors.New("product revision requires a product binding")
		}
		return nil
	}
	var product model.Product
	if err := db.Where("id = ? AND tenant_id = ? AND product_kind = ? AND scenic_area_id = ?", productID, tenantID, "ticket", scenicAreaID).First(&product).Error; err != nil {
		return errors.New("ticket product is not owned by scenic area")
	}
	if productRevisionID != 0 {
		var revision model.ProductRevision
		if err := db.Where("id = ? AND product_id = ? AND tenant_id = ? AND scenic_area_id = ?", productRevisionID, productID, tenantID, scenicAreaID).First(&revision).Error; err != nil {
			return errors.New("product revision is not owned by ticket product")
		}
	}
	return nil
}

func ensureDefaultPrintTemplateTx(tx *gorm.DB, tenantID, scenicAreaID, actorID uint) (*model.PrintTemplate, *model.PrintTemplateRevision, error) {
	var template model.PrintTemplate
	err := tx.Where("tenant_id = ? AND scenic_area_id = ? AND product_id = 0 AND product_revision_id = 0", tenantID, scenicAreaID).First(&template).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		definition := DefaultPrintTemplateDefinition()
		definitionJSON, definitionHash, hashErr := printDefinitionHash(definition)
		if hashErr != nil {
			return nil, nil, hashErr
		}
		template = model.PrintTemplate{TenantID: tenantID, ScenicAreaID: scenicAreaID, Name: "景区默认票据", Status: printTemplateStatusActive, PaperWidthMM: definition.PaperWidthMM, PrinterProfile: "escpos"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&template).Error; err != nil {
			return nil, nil, err
		}
		if template.ID == 0 {
			if err := tx.Where("tenant_id = ? AND scenic_area_id = ? AND product_id = 0 AND product_revision_id = 0", tenantID, scenicAreaID).First(&template).Error; err != nil {
				return nil, nil, err
			}
		}
		var revision model.PrintTemplateRevision
		if err := tx.Where("template_id = ? AND tenant_id = ? AND status = ?", template.ID, tenantID, printRevisionPublished).First(&revision).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			revision = model.PrintTemplateRevision{TenantID: tenantID, ScenicAreaID: scenicAreaID, TemplateID: template.ID, Version: 1, Status: printRevisionPublished, DefinitionJSON: definitionJSON, DefinitionHash: definitionHash, CreatedBy: actorID, PublishedBy: actorID}
			now := time.Now()
			revision.PublishedAt = &now
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&revision).Error; err != nil {
				return nil, nil, err
			}
			if revision.ID == 0 {
				if err := tx.Where("template_id = ? AND tenant_id = ? AND status = ?", template.ID, tenantID, printRevisionPublished).First(&revision).Error; err != nil {
					return nil, nil, err
				}
			}
		}
		if template.CurrentRevisionID == 0 {
			if err := tx.Model(&template).Update("current_revision_id", revision.ID).Error; err != nil {
				return nil, nil, err
			}
			template.CurrentRevisionID = revision.ID
		}
		return &template, &revision, nil
	} else if err != nil {
		return nil, nil, err
	}
	if template.CurrentRevisionID == 0 {
		var revision model.PrintTemplateRevision
		if err := tx.Where("template_id = ? AND tenant_id = ? AND status = ?", template.ID, tenantID, printRevisionPublished).First(&revision).Error; err != nil {
			return nil, nil, err
		}
		if err := tx.Model(&template).Update("current_revision_id", revision.ID).Error; err != nil {
			return nil, nil, err
		}
		template.CurrentRevisionID = revision.ID
		return &template, &revision, nil
	}
	var revision model.PrintTemplateRevision
	if err := tx.Where("id = ? AND template_id = ? AND tenant_id = ? AND status = ?", template.CurrentRevisionID, template.ID, tenantID, printRevisionPublished).First(&revision).Error; err != nil {
		return nil, nil, err
	}
	return &template, &revision, nil
}

type printTicketRenderInput struct {
	OrderNo string
	Scenic  string
	Tickets []model.Ticket
}

func renderSamplePrintDocument(name string, definition model.PrintTemplateDefinition) model.PrintDocument {
	sample := model.Ticket{TicketCode: "SAMPLE-QR-001", VisitorName: "张三", VisitorPhone: "13800138000"}
	useDate := time.Now().AddDate(0, 0, 1)
	item := model.OrderItem{ProductName: "成人票", Price: 128, Quantity: 1, UseDate: &useDate}
	sample.OrderItem = item
	return renderPrintDocument(name, "示例景区", definition, printTicketRenderInput{OrderNo: "ORDER-20260819-0001", Scenic: "示例景区", Tickets: []model.Ticket{sample}})
}

func renderPrintDocument(name, scenic string, definition model.PrintTemplateDefinition, input printTicketRenderInput) model.PrintDocument {
	document := model.PrintDocument{SchemaVersion: 1, PaperWidthMM: definition.PaperWidthMM, TemplateName: name, ScenicArea: scenic, Blocks: make([]model.PrintDocumentBlock, 0, len(definition.Blocks)*maxPrintInt(1, len(input.Tickets)))}
	tickets := input.Tickets
	if len(tickets) == 0 {
		tickets = []model.Ticket{{OrderItem: model.OrderItem{ProductName: "门票"}, TicketCode: input.OrderNo}}
	}
	for index, ticket := range tickets {
		for _, templateBlock := range definition.Blocks {
			text := renderPrintBlockText(templateBlock, scenic, input.OrderNo, ticket, index, len(tickets))
			if text == "" && (templateBlock.Kind == "logo" || templateBlock.Kind == "visitor_name" || templateBlock.Kind == "visitor_phone_suffix" || templateBlock.Kind == "validity" || templateBlock.Kind == "checkpoint_summary" || templateBlock.Kind == "price") {
				continue
			}
			document.Blocks = append(document.Blocks, model.PrintDocumentBlock{Kind: templateBlock.Kind, Text: text, Align: templateBlock.Align, FontSize: templateBlock.FontSize, Bold: templateBlock.Bold, Spacing: templateBlock.Spacing, Separator: templateBlock.Separator})
		}
	}
	return document
}

func renderPrintBlockText(block model.PrintTemplateBlock, scenic, orderNo string, ticket model.Ticket, index, total int) string {
	item := ticket.OrderItem
	phone := strings.TrimSpace(ticket.VisitorPhone)
	if phone == "" {
		phone = strings.TrimSpace(item.VisitorPhone)
	}
	phoneSuffix := phone
	if len(phoneSuffix) > 4 {
		phoneSuffix = phoneSuffix[len(phoneSuffix)-4:]
	}
	switch block.Kind {
	case "scenic_name":
		return scenic
	case "logo":
		return strings.TrimSpace(block.Text)
	case "product_name":
		if item.ProductName != "" {
			return item.ProductName
		}
		return "门票"
	case "use_date":
		if item.UseDate != nil {
			return "使用日期：" + item.UseDate.Format("2006-01-02")
		}
		return "有效期：以订单规则为准"
	case "validity":
		if item.ValidityStart != nil && item.ValidityEnd != nil {
			return fmt.Sprintf("有效期：%s 至 %s", item.ValidityStart.Format("2006-01-02"), item.ValidityEnd.Format("2006-01-02"))
		}
		return ""
	case "visitor_name":
		if ticket.VisitorName != "" {
			return "游客：" + ticket.VisitorName
		}
		if item.VisitorName != "" {
			return "游客：" + item.VisitorName
		}
		return ""
	case "visitor_phone_suffix":
		if phoneSuffix == "" {
			return ""
		}
		return "手机号后四位：" + phoneSuffix
	case "order_no":
		return "订单号：" + orderNo
	case "ticket_code":
		return "票码：" + ticket.TicketCode
	case "qr_code":
		return ticket.TicketCode
	case "barcode":
		return ticket.TicketCode
	case "ticket_sequence":
		return fmt.Sprintf("第 %d / %d 张", index+1, total)
	case "checkpoint_summary":
		return "检票点：按票种规则执行"
	case "price":
		if item.Price > 0 {
			return fmt.Sprintf("售价：¥%.2f", item.Price)
		}
		return ""
	case "custom_text", "footer_text":
		return block.Text
	default:
		return ""
	}
}

func maxPrintInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// buildPrintDocumentTx resolves the current server-side template and renders
// a complete immutable document from order/ticket facts. It is shared by
// normal POS sales and supervised reissue jobs.
func buildPrintDocumentTx(tx *gorm.DB, tenantID, actorID uint, order *model.Order, ticketCode string) (model.PrintTemplateRevision, model.PrintDocument, string, error) {
	if order == nil || order.ID == 0 || tenantID == 0 {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", errors.New("order is required")
	}
	query := tx.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).Preload("OrderItem")
	if strings.TrimSpace(ticketCode) != "" {
		query = query.Where("ticket_code = ?", strings.TrimSpace(ticketCode))
	}
	var tickets []model.Ticket
	if err := query.Order("id ASC").Find(&tickets).Error; err != nil {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", err
	}
	if len(tickets) == 0 {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", errors.New("ticket not found for order")
	}
	scenicAreaID := tickets[0].FulfillmentScenicAreaID
	if scenicAreaID == 0 {
		scenicAreaID = tickets[0].ScenicAreaID
	}
	if scenicAreaID == 0 {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", errors.New("ticket has no scenic area")
	}
	for _, ticket := range tickets[1:] {
		otherArea := ticket.FulfillmentScenicAreaID
		if otherArea == 0 {
			otherArea = ticket.ScenicAreaID
		}
		if otherArea != scenicAreaID {
			return model.PrintTemplateRevision{}, model.PrintDocument{}, "", errors.New("one print job cannot contain tickets from multiple scenic areas")
		}
	}
	// A PrintJob carries one immutable template revision. An order may contain
	// several products, so do not silently render every ticket with the first
	// ticket's product template. The POS queues a separate job per ticket when
	// the order contains mixed products; legacy/order-level callers fail closed
	// until they do the same.
	var template *model.PrintTemplate
	var revision *model.PrintTemplateRevision
	for index := range tickets {
		productID := tickets[index].FulfillmentProductID
		if productID == 0 {
			productID = tickets[index].OrderItem.ProductID
		}
		revisionID := tickets[index].ProductRevisionID
		if revisionID == 0 {
			revisionID = tickets[index].OrderItem.ProductRevisionID
		}
		candidateTemplate, candidateRevision, resolveErr := resolvePrintTemplateTx(tx, tenantID, scenicAreaID, productID, revisionID, actorID)
		if resolveErr != nil {
			return model.PrintTemplateRevision{}, model.PrintDocument{}, "", resolveErr
		}
		if index == 0 {
			template, revision = candidateTemplate, candidateRevision
			continue
		}
		if candidateTemplate.ID != template.ID || candidateRevision.ID != revision.ID {
			return model.PrintTemplateRevision{}, model.PrintDocument{}, "", errors.New("order contains tickets with different print templates; queue each ticket separately")
		}
	}
	definition, err := decodePrintDefinition(revision.DefinitionJSON)
	if err != nil {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", err
	}
	var scenic model.ScenicArea
	if err := tx.Where("id = ? AND tenant_id = ?", scenicAreaID, tenantID).First(&scenic).Error; err != nil {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", err
	}
	// Order items do not expose settlement values in the rendered document.
	// The current template is selected by immutable fulfillment facts only.
	document := renderPrintDocument(template.Name, scenic.Name, *definition, printTicketRenderInput{OrderNo: order.OrderNo, Scenic: scenic.Name, Tickets: tickets})
	encoded, err := json.Marshal(document)
	if err != nil {
		return model.PrintTemplateRevision{}, model.PrintDocument{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return *revision, document, hex.EncodeToString(digest[:]), nil
}

func resolvePrintTemplateTx(tx *gorm.DB, tenantID, scenicAreaID, productID, revisionID, actorID uint) (*model.PrintTemplate, *model.PrintTemplateRevision, error) {
	var candidates []model.PrintTemplate
	if err := tx.Where("tenant_id = ? AND scenic_area_id = ? AND status = ? AND current_revision_id != 0", tenantID, scenicAreaID, printTemplateStatusActive).
		Order("id ASC").Find(&candidates).Error; err != nil {
		return nil, nil, err
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		priority := func(template model.PrintTemplate) int {
			if template.ProductID == productID && template.ProductRevisionID == revisionID && productID != 0 && revisionID != 0 {
				return 0
			}
			if template.ProductID == productID && template.ProductRevisionID == 0 && productID != 0 {
				return 1
			}
			if template.ProductID == 0 && template.ProductRevisionID == 0 {
				return 2
			}
			return 3
		}
		return priority(candidates[left]) < priority(candidates[right])
	})
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.ProductID != 0 && candidate.ProductID != productID {
			continue
		}
		if candidate.ProductRevisionID != 0 && candidate.ProductRevisionID != revisionID {
			continue
		}
		var revision model.PrintTemplateRevision
		if err := tx.Where("id = ? AND template_id = ? AND tenant_id = ? AND status = ?", candidate.CurrentRevisionID, candidate.ID, tenantID, printRevisionPublished).First(&revision).Error; err != nil {
			continue
		}
		return candidate, &revision, nil
	}
	// A tenant may have been upgraded from the previous print-job-only model.
	// Materialize the standard template once, then resolve it like any other.
	return ensureDefaultPrintTemplateTx(tx, tenantID, scenicAreaID, actorID)
}

func buildPrintJobSnapshotTx(tx *gorm.DB, tenantID, deviceID, operatorID, shiftID uint, order *model.Order, ticketCode, afterSaleRequestNo string, reprintOfJobID uint) (*model.PrintJob, error) {
	revision, document, contentHash, err := buildPrintDocumentTx(tx, tenantID, operatorID, order, ticketCode)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	job := &model.PrintJob{TenantID: tenantID, DeviceID: deviceID, OperatorID: operatorID, ShiftID: shiftID, OrderNo: order.OrderNo, TicketCode: strings.TrimSpace(ticketCode), AfterSaleRequestNo: strings.TrimSpace(afterSaleRequestNo), Status: "queued", TemplateRevisionID: revision.ID, PrintDocumentJSON: string(encoded), ContentHash: contentHash, PaperWidthMM: document.PaperWidthMM, CopyCount: 1, ReprintOfJobID: reprintOfJobID}
	return job, nil
}
