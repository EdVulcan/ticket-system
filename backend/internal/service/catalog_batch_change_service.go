package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CatalogBatchPlanPreviewed = "previewed"
	CatalogBatchPlanCompleted = "completed"
	CatalogBatchPlanExpired   = "expired"
	CatalogBatchPlanFailed    = "failed"

	CatalogBatchOpAddCheckpoint    = "add_checkpoints"
	CatalogBatchOpRemoveCheckpoint = "remove_checkpoints"
	CatalogBatchOpSetLimit         = "set_checkpoint_limit"
)

var batchLimitPattern = regexp.MustCompile(`(?:(?:每(?:个|处)?(?:检票点|点)?\s*(?:最多(?:核销|验证|使用)?\s*)?)|(?:(?:上限|限额|最多)(?:核销|验证|使用)?\s*(?:为\s*)?)|(?:(?:设置为|设为)\s*))(\d+)`)

// CatalogRuleOperation is the restricted, server-executable operation DSL.
// The API accepts names for convenience, but Preview always persists IDs.
type CatalogRuleOperation struct {
	Kind            string   `json:"kind"`
	ProductIDs      []uint   `json:"product_ids,omitempty"`
	ProductNames    []string `json:"product_names,omitempty"`
	AllProducts     bool     `json:"all_products,omitempty"`
	CheckpointIDs   []uint   `json:"checkpoint_ids,omitempty"`
	CheckpointNames []string `json:"checkpoint_names,omitempty"`
	GroupName       string   `json:"group_name,omitempty"`
	MaxPerCheckIn   *int     `json:"max_per_check_in,omitempty"`
}

type CatalogBatchChangePreviewRequest struct {
	InputText      string                 `json:"input_text"`
	IdempotencyKey string                 `json:"idempotency_key"`
	Operations     []CatalogRuleOperation `json:"operations,omitempty"`
}

type CatalogBatchChangeLinePreview struct {
	LineID              uint   `json:"line_id"`
	ProductID           uint   `json:"product_id"`
	ProductName         string `json:"product_name"`
	ScenicAreaID        uint   `json:"scenic_area_id"`
	BeforeRevisionID    uint   `json:"before_revision_id"`
	AfterRevisionID     uint   `json:"after_revision_id,omitempty"`
	BeforeJSON          string `json:"before_json"`
	AfterJSON           string `json:"after_json"`
	Status              string `json:"status"`
	AffectedOfferCount  int    `json:"affected_offer_count"`
	AffectedBundleCount int    `json:"affected_bundle_count"`
	ErrorMessage        string `json:"error_message,omitempty"`
}

type CatalogBatchChangePreview struct {
	PlanID         uint                            `json:"plan_id"`
	TenantID       uint                            `json:"tenant_id"`
	InputText      string                          `json:"input_text"`
	Operations     []CatalogRuleOperation          `json:"operations"`
	PlanHash       string                          `json:"plan_hash"`
	IdempotencyKey string                          `json:"idempotency_key"`
	Status         string                          `json:"status"`
	ExpiresAt      time.Time                       `json:"expires_at"`
	ConfirmedAt    *time.Time                      `json:"confirmed_at,omitempty"`
	CompletedAt    *time.Time                      `json:"completed_at,omitempty"`
	CanConfirm     bool                            `json:"can_confirm"`
	Warnings       []string                        `json:"warnings,omitempty"`
	Lines          []CatalogBatchChangeLinePreview `json:"lines"`
}

// CatalogBatchError lets HTTP handlers distinguish an invalid request from a
// stale approval without leaking persistence or tenant details.
type CatalogBatchError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *CatalogBatchError) Error() string { return e.Message }

func batchInvalid(message string) error {
	return &CatalogBatchError{Code: "invalid_request", Message: message, HTTPStatus: 400}
}

func batchNotFound(message string) error {
	return &CatalogBatchError{Code: "not_found", Message: message, HTTPStatus: 404}
}

func batchConflict(message string) error {
	return &CatalogBatchError{Code: "stale_plan", Message: message, HTTPStatus: 409}
}

type catalogRulePreviewGroup struct {
	GroupName       string                   `json:"group_name"`
	MaxTotalCheckIn int                      `json:"max_total_check_in"`
	Items           []catalogRulePreviewItem `json:"items"`
}

type catalogRulePreviewItem struct {
	CheckpointID   uint   `json:"checkpoint_id"`
	CheckpointName string `json:"checkpoint_name"`
	MaxPerCheckIn  int    `json:"max_per_check_in"`
}

type catalogRuleProjection struct {
	Name         string                    `json:"name"`
	ValidityType string                    `json:"validity_type"`
	Groups       []catalogRulePreviewGroup `json:"groups"`
}

type catalogBatchCanonical struct {
	Operations []CatalogRuleOperation `json:"operations"`
}

type catalogBatchHashInput struct {
	Operations []CatalogRuleOperation `json:"operations"`
	Lines      []struct {
		ProductID        uint `json:"product_id"`
		BeforeRevisionID uint `json:"before_revision_id"`
		ScenicAreaID     uint `json:"scenic_area_id"`
	} `json:"lines"`
}

type catalogBatchLineBuild struct {
	Product             model.Product
	BeforeJSON          string
	AfterJSON           string
	Changed             bool
	AffectedOfferCount  int
	AffectedBundleCount int
	Status              string
	ErrorMessage        string
}

type CatalogBatchChangeService struct{}

func (s *CatalogBatchChangeService) Preview(tenantID, actorUserID uint, actorRole string, req CatalogBatchChangePreviewRequest) (*CatalogBatchChangePreview, error) {
	if tenantID == 0 {
		return nil, batchInvalid("tenant is required")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, batchInvalid("idempotency_key is required")
	}
	if len(req.IdempotencyKey) > 120 {
		return nil, batchInvalid("idempotency_key is too long")
	}
	if len([]rune(req.InputText)) > 2000 {
		return nil, batchInvalid("input_text cannot exceed 2000 characters")
	}
	if actorRole == "" {
		actorRole = "admin"
	}
	var response *CatalogBatchChangePreview
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		var existing model.CatalogBatchChangePlan
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, strings.TrimSpace(req.IdempotencyKey)).First(&existing).Error; err == nil {
			if strings.TrimSpace(req.InputText) != existing.InputText {
				return batchConflict("idempotency key already belongs to a different preview")
			}
			if len(req.Operations) > 0 {
				requestedJSON, marshalErr := json.Marshal(catalogBatchCanonical{Operations: canonicalizeRawCatalogOperations(req.Operations)})
				if marshalErr != nil {
					return marshalErr
				}
				if string(requestedJSON) != existing.OperationJSON {
					return batchConflict("idempotency key already belongs to a different operation set")
				}
			} else if strings.TrimSpace(req.InputText) == "" {
				return batchInvalid("input_text or structured operations is required")
			}
			var lines []model.CatalogBatchChangeLine
			if err := tx.Where("plan_id = ? AND tenant_id = ?", existing.ID, tenantID).Order("product_id ASC").Find(&lines).Error; err != nil {
				return err
			}
			response = previewFromPlan(existing, lines)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		products, checkpoints, err := loadCatalogBatchCandidates(tx, tenantID)
		if err != nil {
			return err
		}
		operations := req.Operations
		if len(operations) == 0 {
			operations, err = parseCatalogBatchText(req.InputText, products, checkpoints)
			if err != nil {
				return err
			}
		}
		operations, err = resolveCatalogBatchOperations(tx, tenantID, operations, products, checkpoints)
		if err != nil {
			return err
		}
		if len(operations) == 0 {
			return batchInvalid("at least one catalog operation is required")
		}
		builds, err := buildCatalogBatchLines(tx, tenantID, products, operations)
		if err != nil {
			return err
		}
		if len(builds) == 0 {
			return batchInvalid("the operation did not select any local supplier product")
		}
		canonicalBytes, err := json.Marshal(catalogBatchCanonical{Operations: operations})
		if err != nil {
			return err
		}
		hashInput := catalogBatchHashInput{Operations: operations}
		for _, build := range builds {
			hashInput.Lines = append(hashInput.Lines, struct {
				ProductID        uint `json:"product_id"`
				BeforeRevisionID uint `json:"before_revision_id"`
				ScenicAreaID     uint `json:"scenic_area_id"`
			}{build.Product.ID, build.Product.CurrentRevisionID, build.Product.ScenicAreaID})
		}
		hashBytes, err := json.Marshal(hashInput)
		if err != nil {
			return err
		}
		hashSum := sha256.Sum256(hashBytes)
		planHash := hex.EncodeToString(hashSum[:])
		expiresAt := time.Now().Add(30 * time.Minute)
		plan := model.CatalogBatchChangePlan{
			TenantID: tenantID, ActorUserID: actorUserID, ActorRole: actorRole,
			InputText: strings.TrimSpace(req.InputText), OperationJSON: string(canonicalBytes),
			PlanHash: planHash, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
			Status: CatalogBatchPlanPreviewed, PreviewJSON: "{}", ExpiresAt: expiresAt,
		}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		lines := make([]model.CatalogBatchChangeLine, 0, len(builds))
		for _, build := range builds {
			line := model.CatalogBatchChangeLine{
				PlanID: plan.ID, TenantID: tenantID, ProductID: build.Product.ID,
				ProductName: build.Product.Name, ScenicAreaID: build.Product.ScenicAreaID,
				BeforeRevisionID: build.Product.CurrentRevisionID, BeforeJSON: build.BeforeJSON,
				AfterJSON: build.AfterJSON, Status: build.Status,
				AffectedOfferCount: build.AffectedOfferCount, AffectedBundleCount: build.AffectedBundleCount,
				ErrorMessage: build.ErrorMessage,
			}
			lines = append(lines, line)
		}
		if err := tx.Create(&lines).Error; err != nil {
			return err
		}
		plan.Lines = lines
		response = previewFromPlan(plan, lines)
		response.Operations = operations
		previewJSON, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if err := tx.Model(&plan).Update("preview_json", string(previewJSON)).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "catalog.batch_change.preview", "catalog_batch_change_plan", plan.ID,
			"create catalog batch change preview", "{}", string(previewJSON))
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *CatalogBatchChangeService) Get(tenantID, planID uint) (*CatalogBatchChangePreview, error) {
	if tenantID == 0 || planID == 0 {
		return nil, batchInvalid("tenant and plan are required")
	}
	var plan model.CatalogBatchChangePlan
	if err := model.DB.Where("id = ? AND tenant_id = ?", planID, tenantID).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, batchNotFound("catalog batch change plan not found")
		}
		return nil, err
	}
	var lines []model.CatalogBatchChangeLine
	if err := model.DB.Where("plan_id = ? AND tenant_id = ?", plan.ID, tenantID).Order("product_id ASC").Find(&lines).Error; err != nil {
		return nil, err
	}
	return previewFromPlan(plan, lines), nil
}

func (s *CatalogBatchChangeService) Confirm(tenantID, actorUserID uint, actorRole string, planID uint, planHash string) (*CatalogBatchChangePreview, error) {
	if tenantID == 0 || planID == 0 || strings.TrimSpace(planHash) == "" {
		return nil, batchInvalid("tenant, plan and plan_hash are required")
	}
	if actorRole == "" {
		actorRole = "admin"
	}
	var response *CatalogBatchChangePreview
	var applyErr error
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		var plan model.CatalogBatchChangePlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", planID, tenantID).First(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return batchNotFound("catalog batch change plan not found")
			}
			return err
		}
		var lines []model.CatalogBatchChangeLine
		if err := tx.Where("plan_id = ? AND tenant_id = ?", plan.ID, tenantID).Order("product_id ASC").Find(&lines).Error; err != nil {
			return err
		}
		if plan.Status == CatalogBatchPlanCompleted {
			response = previewFromPlan(plan, lines)
			return nil
		}
		if plan.Status != CatalogBatchPlanPreviewed {
			return batchConflict("catalog batch change plan is no longer executable")
		}
		if !strings.EqualFold(strings.TrimSpace(planHash), plan.PlanHash) {
			return batchConflict("plan_hash does not match the stored preview")
		}
		if time.Now().After(plan.ExpiresAt) {
			plan.Status = CatalogBatchPlanExpired
			plan.ErrorMessage = "catalog batch change plan expired; create a new preview"
			_ = tx.Model(&plan).Updates(map[string]interface{}{"status": plan.Status, "error_message": plan.ErrorMessage}).Error
			applyErr = batchConflict(plan.ErrorMessage)
			return nil
		}
		for _, line := range lines {
			if line.Status != "no_change" && (line.AffectedBundleCount > 0 || strings.TrimSpace(line.ErrorMessage) != "") {
				applyErr = batchConflict(fmt.Sprintf("product %s cannot be changed until its active bundle dependency is revised", line.ProductName))
				return nil
			}
		}
		var canonical catalogBatchCanonical
		if err := json.Unmarshal([]byte(plan.OperationJSON), &canonical); err != nil {
			return err
		}
		operationsByProduct := make(map[uint][]CatalogRuleOperation)
		for _, operation := range canonical.Operations {
			for _, productID := range operation.ProductIDs {
				operationsByProduct[productID] = append(operationsByProduct[productID], operation)
			}
		}
		products := make(map[uint]model.Product, len(lines))
		for _, line := range lines {
			var product model.Product
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ?", line.ProductID, tenantID).First(&product).Error; err != nil {
				return err
			}
			if product.CurrentRevisionID != line.BeforeRevisionID {
				plan.Status = CatalogBatchPlanExpired
				plan.ErrorMessage = fmt.Sprintf("product %s changed after preview; create a new preview", product.Name)
				_ = tx.Model(&plan).Updates(map[string]interface{}{"status": plan.Status, "error_message": plan.ErrorMessage}).Error
				applyErr = batchConflict(plan.ErrorMessage)
				return nil
			}
			beforeJSON, err := projectCatalogRule(&product.Rule)
			if err != nil {
				return err
			}
			if beforeJSON != line.BeforeJSON {
				plan.Status = CatalogBatchPlanExpired
				plan.ErrorMessage = fmt.Sprintf("product %s rule changed after preview; create a new preview", product.Name)
				_ = tx.Model(&plan).Updates(map[string]interface{}{"status": plan.Status, "error_message": plan.ErrorMessage}).Error
				applyErr = batchConflict(plan.ErrorMessage)
				return nil
			}
			products[product.ID] = product
		}
		now := time.Now()
		for i := range lines {
			line := &lines[i]
			product := products[line.ProductID]
			if line.Status == "no_change" {
				continue
			}
			working, err := cloneTicketRule(&product.Rule)
			if err != nil {
				return err
			}
			for _, operation := range operationsByProduct[product.ID] {
				if err := applyCatalogRuleOperation(&working, operation); err != nil {
					return err
				}
			}
			product.Rule = working
			if err := validateProduct(tx, tenantID, &product, &product.Rule); err != nil {
				return err
			}
			if err := assignProductScenicArea(tx, tenantID, &product, &product.Rule); err != nil {
				return err
			}
			afterJSON, err := projectCatalogRule(&product.Rule)
			if err != nil {
				return err
			}
			if afterJSON != line.AfterJSON {
				return batchConflict(fmt.Sprintf("product %s no longer matches the previewed result", product.Name))
			}
			if err := replaceCatalogRuleGroupsTx(tx, product.RuleID, product.Rule.Groups); err != nil {
				return err
			}
			var revised model.Product
			if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ?", product.ID, tenantID).First(&revised).Error; err != nil {
				return err
			}
			newRevision, err := createProductRevisionTx(tx, &revised)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.ProductOffer{}).Where("source_product_id = ? AND supplier_tenant_id = ? AND status = ? AND product_revision_id != ?", product.ID, tenantID, "active", newRevision.ID).Update("product_revision_id", newRevision.ID).Error; err != nil {
				return err
			}
			if err := syncListingsForSourceProductTx(tx, tenantID, product.ID, actorUserID, "catalog batch rule change"); err != nil {
				return err
			}
			if err := tx.Model(line).Updates(map[string]interface{}{"after_revision_id": newRevision.ID, "status": "applied", "error_message": ""}).Error; err != nil {
				return err
			}
			line.AfterRevisionID = newRevision.ID
			line.Status = "applied"
			line.ErrorMessage = ""
			if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "catalog.batch_change.apply", "product", product.ID,
				"apply approved catalog batch rule change", line.BeforeJSON, line.AfterJSON); err != nil {
				return err
			}
		}
		if plan.ConfirmedAt == nil {
			plan.ConfirmedAt = &now
		}
		plan.CompletedAt = &now
		plan.Status = CatalogBatchPlanCompleted
		plan.ErrorMessage = ""
		if err := tx.Model(&plan).Updates(map[string]interface{}{"status": plan.Status, "confirmed_at": plan.ConfirmedAt, "completed_at": plan.CompletedAt, "error_message": ""}).Error; err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "catalog.batch_change.confirm", "catalog_batch_change_plan", plan.ID,
			"confirm and execute catalog batch change", "{\"status\":\"previewed\"}", "{\"status\":\"completed\"}"); err != nil {
			return err
		}
		response = previewFromPlan(plan, lines)
		return nil
	})
	if err != nil {
		if applyErr == nil {
			_ = markCatalogBatchPlanFailed(tenantID, planID, err.Error())
		}
		return nil, err
	}
	if applyErr != nil {
		return nil, applyErr
	}
	return response, nil
}

func markCatalogBatchPlanFailed(tenantID, planID uint, message string) error {
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.CatalogBatchChangePlan{}).Where("id = ? AND tenant_id = ? AND status = ?", planID, tenantID, CatalogBatchPlanPreviewed).Updates(map[string]interface{}{"status": CatalogBatchPlanFailed, "error_message": message}).Error
	})
}

func loadCatalogBatchCandidates(tx *gorm.DB, tenantID uint) ([]model.Product, []model.CheckPoint, error) {
	var products []model.Product
	if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("tenant_id = ?", tenantID).Order("id ASC").Find(&products).Error; err != nil {
		return nil, nil, err
	}
	var checkpoints []model.CheckPoint
	if err := tx.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&checkpoints).Error; err != nil {
		return nil, nil, err
	}
	return products, checkpoints, nil
}

func parseCatalogBatchText(input string, products []model.Product, checkpoints []model.CheckPoint) ([]CatalogRuleOperation, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, batchInvalid("input_text or structured operations is required")
	}
	clauses := strings.FieldsFunc(input, func(r rune) bool { return r == '；' || r == ';' || r == '\n' })
	operations := make([]CatalogRuleOperation, 0, len(clauses))
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		kind := ""
		switch {
		case strings.Contains(clause, "删除") || strings.Contains(clause, "移除") || strings.Contains(clause, "去掉") || strings.Contains(clause, "取消"):
			kind = CatalogBatchOpRemoveCheckpoint
		case strings.Contains(clause, "设置") && (strings.Contains(clause, "次数") || strings.Contains(clause, "上限") || strings.Contains(clause, "限额")):
			kind = CatalogBatchOpSetLimit
		case strings.Contains(clause, "增加") || strings.Contains(clause, "添加") || strings.Contains(clause, "新增") || strings.Contains(clause, "加上"):
			kind = CatalogBatchOpAddCheckpoint
		default:
			return nil, batchInvalid("无法识别操作，请使用增加、移除或设置检票次数")
		}
		operation := CatalogRuleOperation{Kind: kind}
		for _, product := range products {
			if isDistributedListing(&product) {
				continue
			}
			if strings.Contains(clause, product.Name) {
				operation.ProductNames = append(operation.ProductNames, product.Name)
			}
		}
		if len(operation.ProductNames) == 0 && (strings.Contains(clause, "所有票种") || strings.Contains(clause, "全部票种") || strings.Contains(clause, "所有门票") || strings.Contains(clause, "全部门票")) {
			operation.AllProducts = true
		}
		for _, checkpoint := range checkpoints {
			if strings.Contains(clause, checkpoint.Name) {
				operation.CheckpointNames = append(operation.CheckpointNames, checkpoint.Name)
			}
		}
		if len(operation.ProductNames) == 0 && !operation.AllProducts {
			return nil, batchInvalid("没有匹配到票种名称；请填写准确的票种名称或使用结构化选择")
		}
		if len(operation.CheckpointNames) == 0 {
			return nil, batchInvalid("没有匹配到检票点名称；请填写准确的检票点名称或使用结构化选择")
		}
		if operation.Kind == CatalogBatchOpSetLimit {
			matches := batchLimitPattern.FindStringSubmatch(clause)
			if len(matches) != 2 {
				return nil, batchInvalid("设置检票次数时必须给出正整数上限")
			}
			limit, err := strconv.Atoi(matches[1])
			if err != nil || limit <= 0 || limit > 1000 {
				return nil, batchInvalid("检票次数上限必须在 1 到 1000 之间")
			}
			operation.MaxPerCheckIn = &limit
		} else if operation.Kind == CatalogBatchOpAddCheckpoint {
			// An add instruction may also carry the per-checkpoint limit, for
			// example "增加北门检票点，每个点最多核销 2 次". Keep the
			// default of one when no limit is stated.
			if matches := batchLimitPattern.FindStringSubmatch(clause); len(matches) == 2 {
				limit, err := strconv.Atoi(matches[1])
				if err != nil || limit <= 0 || limit > 1000 {
					return nil, batchInvalid("检票次数上限必须在 1 到 1000 之间")
				}
				operation.MaxPerCheckIn = &limit
			}
		}
		operations = append(operations, operation)
	}
	if len(operations) == 0 {
		return nil, batchInvalid("没有可执行的操作")
	}
	return operations, nil
}

func resolveCatalogBatchOperations(tx *gorm.DB, tenantID uint, operations []CatalogRuleOperation, products []model.Product, checkpoints []model.CheckPoint) ([]CatalogRuleOperation, error) {
	productByID := make(map[uint]model.Product, len(products))
	productByName := make(map[string][]model.Product)
	for _, product := range products {
		if isDistributedListing(&product) {
			continue
		}
		productByID[product.ID] = product
		productByName[product.Name] = append(productByName[product.Name], product)
	}
	checkpointByID := make(map[uint]model.CheckPoint, len(checkpoints))
	checkpointByName := make(map[string][]model.CheckPoint)
	for _, checkpoint := range checkpoints {
		checkpointByID[checkpoint.ID] = checkpoint
		checkpointByName[checkpoint.Name] = append(checkpointByName[checkpoint.Name], checkpoint)
	}
	if len(operations) > 50 {
		return nil, batchInvalid("a preview may contain at most 50 operations")
	}
	resolved := make([]CatalogRuleOperation, 0, len(operations))
	for _, input := range operations {
		operation := input
		operation.Kind = strings.TrimSpace(operation.Kind)
		if operation.Kind != CatalogBatchOpAddCheckpoint && operation.Kind != CatalogBatchOpRemoveCheckpoint && operation.Kind != CatalogBatchOpSetLimit {
			return nil, batchInvalid("unsupported catalog operation")
		}
		productIDs := append([]uint(nil), operation.ProductIDs...)
		if operation.AllProducts {
			productIDs = productIDs[:0]
			for id := range productByID {
				productIDs = append(productIDs, id)
			}
		}
		for _, name := range operation.ProductNames {
			matches := productByName[strings.TrimSpace(name)]
			if len(matches) == 0 {
				return nil, batchInvalid(fmt.Sprintf("票种 %q 不属于当前租户或不存在", name))
			}
			if len(matches) > 1 {
				return nil, batchInvalid(fmt.Sprintf("票种 %q 不唯一，请使用结构化选择", name))
			}
			productIDs = append(productIDs, matches[0].ID)
		}
		productIDs = uniqueSortedIDs(productIDs)
		if len(productIDs) == 0 || len(productIDs) > 100 {
			return nil, batchInvalid("一次操作必须选择 1 到 100 个本租户票种")
		}
		checkpointIDs := append([]uint(nil), operation.CheckpointIDs...)
		for _, name := range operation.CheckpointNames {
			matches := checkpointByName[strings.TrimSpace(name)]
			if len(matches) == 0 {
				return nil, batchInvalid(fmt.Sprintf("检票点 %q 不属于当前租户或不存在", name))
			}
			if len(matches) > 1 {
				return nil, batchInvalid(fmt.Sprintf("检票点 %q 不唯一，请使用结构化选择", name))
			}
			checkpointIDs = append(checkpointIDs, matches[0].ID)
		}
		checkpointIDs = uniqueSortedIDs(checkpointIDs)
		if len(checkpointIDs) == 0 || len(checkpointIDs) > 20 {
			return nil, batchInvalid("一次操作必须选择 1 到 20 个检票点")
		}
		for _, productID := range productIDs {
			product, ok := productByID[productID]
			if !ok || product.TenantID != tenantID {
				return nil, batchInvalid("票种不属于当前租户")
			}
			for _, checkpointID := range checkpointIDs {
				checkpoint, ok := checkpointByID[checkpointID]
				if !ok || checkpoint.TenantID != tenantID || checkpoint.ScenicAreaID != product.ScenicAreaID {
					return nil, batchInvalid(fmt.Sprintf("票种 %s 与检票点 %d 不属于同一租户和景区", product.Name, checkpointID))
				}
			}
		}
		if operation.Kind == CatalogBatchOpSetLimit {
			if operation.MaxPerCheckIn == nil || *operation.MaxPerCheckIn <= 0 || *operation.MaxPerCheckIn > 1000 {
				return nil, batchInvalid("设置检票次数时必须提供 1 到 1000 的 max_per_check_in")
			}
		}
		operation.ProductIDs = productIDs
		operation.ProductNames = nil
		operation.AllProducts = false
		operation.CheckpointIDs = checkpointIDs
		operation.CheckpointNames = nil
		operation.GroupName = strings.TrimSpace(operation.GroupName)
		resolved = append(resolved, operation)
	}
	return resolved, nil
}

func buildCatalogBatchLines(tx *gorm.DB, tenantID uint, products []model.Product, operations []CatalogRuleOperation) ([]catalogBatchLineBuild, error) {
	productByID := make(map[uint]model.Product, len(products))
	selected := make(map[uint]struct{})
	for _, operation := range operations {
		for _, productID := range operation.ProductIDs {
			selected[productID] = struct{}{}
		}
	}
	for _, product := range products {
		if _, ok := selected[product.ID]; ok {
			productByID[product.ID] = product
		}
	}
	ids := make([]uint, 0, len(productByID))
	for id := range productByID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	builds := make([]catalogBatchLineBuild, 0, len(ids))
	for _, id := range ids {
		product := productByID[id]
		if product.CurrentRevisionID == 0 {
			return nil, batchInvalid(fmt.Sprintf("票种 %s 没有当前规则版本", product.Name))
		}
		beforeJSON, err := projectCatalogRule(&product.Rule)
		if err != nil {
			return nil, err
		}
		working, err := cloneTicketRule(&product.Rule)
		if err != nil {
			return nil, err
		}
		for _, operation := range operations {
			if !containsID(operation.ProductIDs, id) {
				continue
			}
			if err := applyCatalogRuleOperation(&working, operation); err != nil {
				return nil, fmt.Errorf("票种 %s: %w", product.Name, err)
			}
		}
		if !catalogRuleHasItems(&working) {
			return nil, fmt.Errorf("票种 %s 至少需要保留一个检票点", product.Name)
		}
		product.Rule = working
		if err := validateProduct(tx, tenantID, &product, &product.Rule); err != nil {
			return nil, fmt.Errorf("票种 %s: %w", product.Name, err)
		}
		if err := assignProductScenicArea(tx, tenantID, &product, &product.Rule); err != nil {
			return nil, fmt.Errorf("票种 %s: %w", product.Name, err)
		}
		afterJSON, err := projectCatalogRule(&product.Rule)
		if err != nil {
			return nil, err
		}
		changed := beforeJSON != afterJSON
		affectedOffers := int64(0)
		if err := tx.Model(&model.ProductOffer{}).Where("supplier_tenant_id = ? AND source_product_id = ? AND status = ?", tenantID, product.ID, "active").Count(&affectedOffers).Error; err != nil {
			return nil, err
		}
		affectedBundles := int64(0)
		if err := tx.Table("bundle_components AS component").Joins("JOIN bundle_versions AS version ON version.id = component.bundle_version_id AND version.status = ?", "active").Joins("JOIN bundle_products AS bundle ON bundle.current_version_id = version.id").Where("component.source_product_id = ? AND component.product_revision_id = ?", product.ID, product.CurrentRevisionID).Distinct("bundle.id").Count(&affectedBundles).Error; err != nil {
			return nil, err
		}
		build := catalogBatchLineBuild{Product: product, BeforeJSON: beforeJSON, AfterJSON: afterJSON, Changed: changed, AffectedOfferCount: int(affectedOffers), AffectedBundleCount: int(affectedBundles)}
		if changed {
			build.Status = "pending"
			if affectedBundles > 0 {
				build.ErrorMessage = "active bundle version references this revision; the distributor must revise the bundle first"
			}
		} else {
			build.Status = "no_change"
		}
		builds = append(builds, build)
	}
	return builds, nil
}

func applyCatalogRuleOperation(rule *model.TicketRule, operation CatalogRuleOperation) error {
	if rule == nil {
		return errors.New("ticket rule is required")
	}
	groups := make([]*model.RuleGroup, 0)
	for i := range rule.Groups {
		if operation.GroupName == "" || rule.Groups[i].GroupName == operation.GroupName {
			groups = append(groups, &rule.Groups[i])
		}
	}
	if operation.GroupName != "" && len(groups) == 0 {
		return fmt.Errorf("rule group %q does not exist", operation.GroupName)
	}
	if operation.Kind == CatalogBatchOpAddCheckpoint && operation.GroupName == "" {
		if len(rule.Groups) != 1 {
			return errors.New("adding a checkpoint requires group_name when a product has multiple rule groups")
		}
		groups = []*model.RuleGroup{&rule.Groups[0]}
	}
	if operation.Kind == CatalogBatchOpSetLimit && operation.GroupName == "" && len(rule.Groups) > 1 {
		groups = ruleGroupsContainingAll(rule, operation.CheckpointIDs)
	}
	for _, checkpointID := range operation.CheckpointIDs {
		switch operation.Kind {
		case CatalogBatchOpAddCheckpoint:
			if err := addCatalogCheckpoint(groups, rule, checkpointID, operation.MaxPerCheckIn); err != nil {
				return err
			}
		case CatalogBatchOpRemoveCheckpoint:
			removeCatalogCheckpoint(groups, checkpointID)
		case CatalogBatchOpSetLimit:
			if operation.MaxPerCheckIn == nil {
				return errors.New("max_per_check_in is required")
			}
			if !setCatalogCheckpointLimit(groups, checkpointID, *operation.MaxPerCheckIn) {
				return fmt.Errorf("checkpoint %d is not in the selected rule group", checkpointID)
			}
		default:
			return errors.New("unsupported catalog operation")
		}
	}
	if operation.Kind == CatalogBatchOpRemoveCheckpoint {
		compactRuleGroups(rule)
	}
	return nil
}

func ruleGroupsContainingAll(rule *model.TicketRule, checkpointIDs []uint) []*model.RuleGroup {
	result := make([]*model.RuleGroup, 0)
	for i := range rule.Groups {
		for _, checkpointID := range checkpointIDs {
			for _, item := range rule.Groups[i].Items {
				if item.CheckPointID == checkpointID {
					result = append(result, &rule.Groups[i])
					break
				}
			}
		}
	}
	return result
}

func addCatalogCheckpoint(groups []*model.RuleGroup, rule *model.TicketRule, checkpointID uint, limit *int) error {
	var existing *model.RuleItem
	var existingGroup *model.RuleGroup
	for i := range rule.Groups {
		for j := range rule.Groups[i].Items {
			if rule.Groups[i].Items[j].CheckPointID == checkpointID {
				existing = &rule.Groups[i].Items[j]
				existingGroup = &rule.Groups[i]
				break
			}
		}
	}
	if existing != nil {
		allowed := false
		for _, group := range groups {
			if group == existingGroup {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("checkpoint %d already belongs to another rule group", checkpointID)
		}
		if limit != nil {
			existing.MaxPerCheckIn = *limit
		}
		return nil
	}
	if len(groups) != 1 {
		return fmt.Errorf("checkpoint %d requires exactly one target rule group", checkpointID)
	}
	max := 1
	if limit != nil {
		max = *limit
	}
	groups[0].Items = append(groups[0].Items, model.RuleItem{CheckPointID: checkpointID, MaxPerCheckIn: max})
	return nil
}

func removeCatalogCheckpoint(groups []*model.RuleGroup, checkpointID uint) {
	for _, group := range groups {
		items := group.Items[:0]
		for _, item := range group.Items {
			if item.CheckPointID != checkpointID {
				items = append(items, item)
			}
		}
		group.Items = items
	}
}

func setCatalogCheckpointLimit(groups []*model.RuleGroup, checkpointID uint, limit int) bool {
	updated := false
	for _, group := range groups {
		for i := range group.Items {
			if group.Items[i].CheckPointID == checkpointID {
				group.Items[i].MaxPerCheckIn = limit
				updated = true
			}
		}
	}
	return updated
}

func compactRuleGroups(rule *model.TicketRule) {
	groups := rule.Groups[:0]
	for _, group := range rule.Groups {
		if len(group.Items) > 0 {
			groups = append(groups, group)
		}
	}
	rule.Groups = groups
}

func catalogRuleHasItems(rule *model.TicketRule) bool {
	if rule == nil {
		return false
	}
	for _, group := range rule.Groups {
		if len(group.Items) > 0 {
			return true
		}
	}
	return false
}

func replaceCatalogRuleGroupsTx(tx *gorm.DB, ruleID uint, groups []model.RuleGroup) error {
	if ruleID == 0 {
		return errors.New("ticket rule is required")
	}
	var existing []model.RuleGroup
	if err := tx.Where("rule_id = ?", ruleID).Find(&existing).Error; err != nil {
		return err
	}
	for _, group := range existing {
		if err := tx.Where("group_id = ?", group.ID).Delete(&model.RuleItem{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("rule_id = ?", ruleID).Delete(&model.RuleGroup{}).Error; err != nil {
		return err
	}
	for _, group := range groups {
		group.ID = 0
		group.RuleID = ruleID
		for i := range group.Items {
			group.Items[i].ID = 0
			group.Items[i].GroupID = 0
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
	}
	return nil
}

func cloneTicketRule(rule *model.TicketRule) (model.TicketRule, error) {
	if rule == nil {
		return model.TicketRule{}, errors.New("ticket rule is required")
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return model.TicketRule{}, err
	}
	var clone model.TicketRule
	if err := json.Unmarshal(data, &clone); err != nil {
		return model.TicketRule{}, err
	}
	return clone, nil
}

func projectCatalogRule(rule *model.TicketRule) (string, error) {
	projection := catalogRuleProjection{}
	if rule != nil {
		projection.Name = rule.Name
		projection.ValidityType = rule.ValidityType
		projection.Groups = make([]catalogRulePreviewGroup, 0, len(rule.Groups))
		for _, group := range rule.Groups {
			itemProjection := make([]catalogRulePreviewItem, 0, len(group.Items))
			for _, item := range group.Items {
				itemProjection = append(itemProjection, catalogRulePreviewItem{CheckpointID: item.CheckPointID, CheckpointName: item.CheckPoint.Name, MaxPerCheckIn: item.MaxPerCheckIn})
			}
			sort.Slice(itemProjection, func(i, j int) bool { return itemProjection[i].CheckpointID < itemProjection[j].CheckpointID })
			projection.Groups = append(projection.Groups, catalogRulePreviewGroup{GroupName: group.GroupName, MaxTotalCheckIn: group.MaxTotalCheckIn, Items: itemProjection})
		}
		sort.Slice(projection.Groups, func(i, j int) bool { return projection.Groups[i].GroupName < projection.Groups[j].GroupName })
	}
	data, err := json.Marshal(projection)
	return string(data), err
}

func previewFromPlan(plan model.CatalogBatchChangePlan, lines []model.CatalogBatchChangeLine) *CatalogBatchChangePreview {
	var preview CatalogBatchChangePreview
	if strings.TrimSpace(plan.PreviewJSON) != "" {
		_ = json.Unmarshal([]byte(plan.PreviewJSON), &preview)
	}
	preview.PlanID = plan.ID
	preview.TenantID = plan.TenantID
	preview.InputText = plan.InputText
	preview.PlanHash = plan.PlanHash
	preview.IdempotencyKey = plan.IdempotencyKey
	preview.Status = plan.Status
	preview.ExpiresAt = plan.ExpiresAt
	preview.ConfirmedAt = plan.ConfirmedAt
	preview.CompletedAt = plan.CompletedAt
	preview.Lines = make([]CatalogBatchChangeLinePreview, 0, len(lines))
	preview.CanConfirm = plan.Status == CatalogBatchPlanPreviewed && time.Now().Before(plan.ExpiresAt)
	for _, line := range lines {
		preview.Lines = append(preview.Lines, CatalogBatchChangeLinePreview{
			LineID: line.ID, ProductID: line.ProductID, ProductName: line.ProductName, ScenicAreaID: line.ScenicAreaID,
			BeforeRevisionID: line.BeforeRevisionID, AfterRevisionID: line.AfterRevisionID, BeforeJSON: line.BeforeJSON,
			AfterJSON: line.AfterJSON, Status: line.Status, AffectedOfferCount: line.AffectedOfferCount,
			AffectedBundleCount: line.AffectedBundleCount, ErrorMessage: line.ErrorMessage,
		})
		if line.Status != "no_change" && (line.ErrorMessage != "" || line.AffectedBundleCount > 0) {
			preview.CanConfirm = false
		}
	}
	return &preview
}

func uniqueSortedIDs(input []uint) []uint {
	seen := make(map[uint]struct{}, len(input))
	result := make([]uint, 0, len(input))
	for _, id := range input {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func canonicalizeRawCatalogOperations(operations []CatalogRuleOperation) []CatalogRuleOperation {
	result := make([]CatalogRuleOperation, len(operations))
	for i, operation := range operations {
		result[i] = operation
		result[i].ProductIDs = uniqueSortedIDs(operation.ProductIDs)
		result[i].CheckpointIDs = uniqueSortedIDs(operation.CheckpointIDs)
		result[i].ProductNames = nil
		result[i].CheckpointNames = nil
		result[i].GroupName = strings.TrimSpace(operation.GroupName)
	}
	return result
}

func containsID(ids []uint, target uint) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
