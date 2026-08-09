package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TeamService struct{}

type TeamAgentInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	JobNumber string `json:"job_number"`
}

type TeamGuideInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	LicenseNo string `json:"license_no"`
}

type TeamVehicleInput struct {
	PlateNumber string `json:"plate_number"`
	DriverName  string `json:"driver_name"`
	DriverPhone string `json:"driver_phone"`
	Capacity    int    `json:"capacity"`
}

// TeamGroupPlanUpdate intentionally exposes only operational plan fields.
// Supplier, scenic-area, contract, order, roster, ticket and money facts are
// maintained by their dedicated workflows and cannot be patched here.
type TeamGroupPlanUpdate struct {
	Name      *string    `json:"name"`
	VisitDate *time.Time `json:"visit_date"`
	GuideID   *uint      `json:"guide_id"`
	VehicleID *uint      `json:"vehicle_id"`
	AgentID   *uint      `json:"agent_id"`
	Reason    string     `json:"reason"`
}

// TeamGroupListOptions contains only tenant-scoped operational filters. The
// tenant itself is always derived from the authenticated request context.
type TeamGroupListOptions struct {
	Page       int
	PageSize   int
	Keyword    string
	Status     string
	VisitStart *time.Time
	VisitEnd   *time.Time
}

type teamGroupPlanAuditSnapshot struct {
	Name         string    `json:"name"`
	VisitDate    time.Time `json:"visit_date"`
	GuideID      uint      `json:"guide_id"`
	VehicleID    uint      `json:"vehicle_id"`
	AgentID      uint      `json:"agent_id"`
	Status       string    `json:"status"`
	SalesOrderID uint      `json:"sales_order_id"`
}

type TeamPriceRule struct {
	ProductID      uint   `json:"product_id"`
	ProductName    string `json:"product_name,omitempty"`
	ScenicAreaID   uint   `json:"scenic_area_id,omitempty"`
	ScenicAreaName string `json:"scenic_area_name,omitempty"`
	PriceCents     int64  `json:"price_cents"`
	MaxQuantity    int    `json:"max_quantity"`
}

type TravelContractInput struct {
	TravelTenantID   uint            `json:"travel_tenant_id"`
	ContractNo       string          `json:"contract_no"`
	Status           string          `json:"status"`
	SettlementDays   int             `json:"settlement_days"`
	CreditLimitCents int64           `json:"credit_limit_cents"`
	StartsAt         *time.Time      `json:"starts_at"`
	EndsAt           *time.Time      `json:"ends_at"`
	PriceRules       []TeamPriceRule `json:"price_rules"`
}

type TravelContractView struct {
	model.TravelContract
	TravelTenantName   string          `json:"travel_tenant_name"`
	SupplierTenantName string          `json:"supplier_tenant_name"`
	PriceRules         []TeamPriceRule `json:"price_rules"`
}

type TravelContractPartner struct {
	TenantID       uint   `json:"tenant_id"`
	Name           string `json:"name"`
	SystemCode     string `json:"system_code"`
	RelationshipID uint   `json:"relationship_id"`
}

type TeamSupplierPartner struct {
	RelationshipID   uint       `json:"relationship_id"`
	SupplierTenantID uint       `json:"supplier_tenant_id"`
	SupplierName     string     `json:"supplier_name"`
	SupplierCode     string     `json:"supplier_code"`
	Contact          string     `json:"contact"`
	Status           string     `json:"status"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
}

type TeamTravelAgencyPartner struct {
	RelationshipID uint       `json:"relationship_id"`
	TravelTenantID uint       `json:"travel_tenant_id"`
	TravelName     string     `json:"travel_name"`
	TravelCode     string     `json:"travel_code"`
	Contact        string     `json:"contact"`
	Phone          string     `json:"phone"`
	Status         string     `json:"status"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

type TeamContractProduct struct {
	ID             uint    `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Price          float64 `json:"price"`
	ScenicAreaID   uint    `json:"scenic_area_id"`
	ScenicAreaName string  `json:"scenic_area_name"`
}

func eligibleTeamContractProductsTx(tx *gorm.DB, supplierTenantID uint) *gorm.DB {
	return tx.Model(&model.Product{}).
		Where("products.tenant_id = ?", supplierTenantID).
		Where("products.status = ?", "online").
		Where("products.source_product_id = 0 AND products.product_offer_id = 0").
		Where("products.code_mode = ?", "ticket").
		Where("products.region_limit IS NULL OR BTRIM(products.region_limit) = ''").
		Where("products.scenic_area_id > 0")
}

func (s *TeamService) ListContractProducts(supplierTenantID uint) ([]TeamContractProduct, error) {
	if err := requireActiveTenantCapability(model.DB, supplierTenantID, "supplier"); err != nil {
		return nil, err
	}
	var products []TeamContractProduct
	err := eligibleTeamContractProductsTx(model.DB, supplierTenantID).
		Select("products.id, products.name, products.type, products.price, products.scenic_area_id, scenic_areas.name AS scenic_area_name").
		Joins("JOIN scenic_areas ON scenic_areas.id = products.scenic_area_id AND scenic_areas.tenant_id = products.tenant_id AND scenic_areas.status = ? AND scenic_areas.deleted_at IS NULL", "active").
		Order("scenic_areas.name, products.name, products.id").
		Scan(&products).Error
	return products, err
}

func (s *TeamService) SearchSupplierPartner(travelTenantID uint, systemCode string) (*TeamSupplierPartner, error) {
	if err := requireActiveTenantCapability(model.DB, travelTenantID, "travel_agency"); err != nil {
		return nil, err
	}
	supplier, err := (&DistributionService{}).GetSupplierByCode(systemCode)
	if err != nil {
		return nil, err
	}
	if supplier.ID == travelTenantID {
		return nil, errors.New("a tenant cannot partner with itself")
	}
	view := &TeamSupplierPartner{
		SupplierTenantID: supplier.ID, SupplierName: supplier.Name, SupplierCode: supplier.SystemCode,
		Contact: supplier.Contact,
	}
	var relationship model.DistributorRelationship
	if err := model.DB.Where("agent_tenant_id = ? AND supplier_tenant_id = ?", travelTenantID, supplier.ID).First(&relationship).Error; err == nil {
		view.RelationshipID = relationship.ID
		view.Status = relationship.TravelStatus
		view.CreatedAt = relationship.TravelAppliedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return view, nil
}

func (s *TeamService) ApplySupplierPartner(travelTenantID uint, systemCode string) error {
	return applySupplierRelationship(travelTenantID, systemCode, "travel_agency", 0, "", "")
}

func (s *TeamService) ApplySupplierPartnerAudited(travelTenantID, operatorID uint, actorRole, systemCode string) error {
	return applySupplierRelationship(travelTenantID, systemCode, "travel_agency", operatorID, actorRole, "team.partner.apply")
}

func (s *TeamService) ListSupplierPartners(travelTenantID uint) ([]TeamSupplierPartner, error) {
	if err := requireActiveTenantCapability(model.DB, travelTenantID, "travel_agency"); err != nil {
		return nil, err
	}
	var relationships []model.DistributorRelationship
	if err := model.DB.Preload("SupplierTenant").Where("agent_tenant_id = ? AND travel_status != ?", travelTenantID, "none").Order("created_at DESC").Find(&relationships).Error; err != nil {
		return nil, err
	}
	rows := make([]TeamSupplierPartner, 0, len(relationships))
	for _, relationship := range relationships {
		if err := requireActiveTenantCapability(model.DB, relationship.SupplierTenantID, "supplier"); err != nil {
			continue
		}
		rows = append(rows, TeamSupplierPartner{
			RelationshipID: relationship.ID, SupplierTenantID: relationship.SupplierTenantID,
			SupplierName: relationship.SupplierTenant.Name, SupplierCode: relationship.SupplierTenant.SystemCode,
			Contact: relationship.SupplierTenant.Contact, Status: relationship.TravelStatus, CreatedAt: relationship.TravelAppliedAt,
		})
	}
	return rows, nil
}

func (s *TeamService) ListTravelAgencyPartners(supplierTenantID uint) ([]TeamTravelAgencyPartner, error) {
	if err := requireActiveTenantCapability(model.DB, supplierTenantID, "supplier"); err != nil {
		return nil, err
	}
	var relationships []model.DistributorRelationship
	if err := model.DB.Preload("AgentTenant").Where("supplier_tenant_id = ? AND travel_status != ?", supplierTenantID, "none").Order("created_at DESC").Find(&relationships).Error; err != nil {
		return nil, err
	}
	rows := make([]TeamTravelAgencyPartner, 0, len(relationships))
	for _, relationship := range relationships {
		if err := requireActiveTenantCapability(model.DB, relationship.AgentTenantID, "travel_agency"); err != nil {
			continue
		}
		rows = append(rows, TeamTravelAgencyPartner{
			RelationshipID: relationship.ID, TravelTenantID: relationship.AgentTenantID,
			TravelName: relationship.AgentTenant.Name, TravelCode: relationship.AgentTenant.SystemCode,
			Contact: relationship.AgentTenant.Contact, Phone: relationship.AgentTenant.Phone,
			Status: relationship.TravelStatus, CreatedAt: relationship.TravelAppliedAt,
		})
	}
	return rows, nil
}

func (s *TeamService) AuditTravelAgencyPartner(supplierTenantID, relationshipID uint, status string) error {
	return auditSupplierRelationship(supplierTenantID, relationshipID, "travel_agency", status, 0, "", "")
}

func (s *TeamService) AuditTravelAgencyPartnerAudited(supplierTenantID, relationshipID, operatorID uint, actorRole, status string) error {
	return auditSupplierRelationship(supplierTenantID, relationshipID, "travel_agency", status, operatorID, actorRole, "team.partner.audit")
}

type TeamOrderInput struct {
	ProductID    uint   `json:"product_id"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
}

func normalizeTeamPriceRulesTx(tx *gorm.DB, supplierTenantID uint, rules []TeamPriceRule) ([]TeamPriceRule, string, error) {
	if len(rules) == 0 {
		return nil, "", errors.New("at least one contract product price is required")
	}
	seen := make(map[uint]struct{}, len(rules))
	normalized := make([]TeamPriceRule, 0, len(rules))
	for _, rule := range rules {
		if rule.ProductID == 0 || rule.PriceCents <= 0 || rule.MaxQuantity < 0 {
			return nil, "", errors.New("invalid contract product price")
		}
		if _, ok := seen[rule.ProductID]; ok {
			return nil, "", errors.New("contract product cannot be repeated")
		}
		var product model.Product
		if err := eligibleTeamContractProductsTx(tx, supplierTenantID).
			Select("products.id", "products.name", "products.scenic_area_id").
			Where("products.id = ?", rule.ProductID).First(&product).Error; err != nil {
			return nil, "", fmt.Errorf("合同产品 %d 当前不可用", rule.ProductID)
		}
		seen[rule.ProductID] = struct{}{}
		var area model.ScenicArea
		_ = tx.Select("id", "name").Where("id = ? AND tenant_id = ?", product.ScenicAreaID, supplierTenantID).First(&area).Error
		normalized = append(normalized, TeamPriceRule{ProductID: product.ID, ProductName: product.Name, ScenicAreaID: product.ScenicAreaID, ScenicAreaName: area.Name, PriceCents: rule.PriceCents, MaxQuantity: rule.MaxQuantity})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ProductID < normalized[j].ProductID })
	stored := make([]TeamPriceRule, len(normalized))
	copy(stored, normalized)
	for i := range stored {
		stored[i].ProductName = ""
		stored[i].ScenicAreaID = 0
		stored[i].ScenicAreaName = ""
	}
	data, err := json.Marshal(stored)
	return normalized, string(data), err
}

func validateTravelContractInput(input TravelContractInput) error {
	if input.TravelTenantID == 0 || strings.TrimSpace(input.ContractNo) == "" {
		return errors.New("travel agency and contract number are required")
	}
	if input.SettlementDays < 0 || input.CreditLimitCents < 0 {
		return errors.New("settlement days and credit limit cannot be negative")
	}
	if input.Status != "" && input.Status != "active" && input.Status != "suspended" {
		return errors.New("invalid contract status")
	}
	if input.StartsAt != nil && input.EndsAt != nil && input.EndsAt.Before(*input.StartsAt) {
		return errors.New("contract end date cannot be before start date")
	}
	return nil
}

func (s *TeamService) CreateContract(supplierTenantID, operatorID uint, input TravelContractInput) (*TravelContractView, error) {
	if err := validateTravelContractInput(input); err != nil {
		return nil, err
	}
	var contract model.TravelContract
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
		if err := requireActiveTenantCapability(tx, input.TravelTenantID, "travel_agency"); err != nil {
			return errors.New("travel agency tenant is unavailable")
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND travel_status = ?", input.TravelTenantID, supplierTenantID, "active").First(&relationship).Error; err != nil {
			return errors.New("active travel agency relationship not found")
		}
		_, priceJSON, err := normalizeTeamPriceRulesTx(tx, supplierTenantID, input.PriceRules)
		if err != nil {
			return err
		}
		status := input.Status
		if status == "" {
			status = "active"
		}
		contract = model.TravelContract{
			TravelTenantID: input.TravelTenantID, SupplierTenantID: supplierTenantID,
			ContractNo: strings.TrimSpace(input.ContractNo), Status: status,
			SettlementDays: input.SettlementDays, CreditLimitCents: input.CreditLimitCents,
			PriceRulesJSON: priceJSON, StartsAt: input.StartsAt, EndsAt: input.EndsAt,
		}
		if err := tx.Create(&contract).Error; err != nil {
			return err
		}
		after, _ := json.Marshal(input)
		return recordAuditTx(tx, operatorID, supplierTenantID, "admin", "tenant", "travel_contract.create", "travel_contract", contract.ID, "供应商创建旅行社合同", "{}", string(after))
	})
	if err != nil {
		return nil, err
	}
	view, err := s.GetContract(supplierTenantID, contract.ID)
	return view, err
}

func (s *TeamService) UpdateContract(supplierTenantID, contractID, operatorID uint, input TravelContractInput) (*TravelContractView, error) {
	if contractID == 0 {
		return nil, errors.New("contract is required")
	}
	if err := validateTravelContractInput(input); err != nil {
		return nil, err
	}
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
		var contract model.TravelContract
		if err := tx.Where("id = ? AND supplier_tenant_id = ?", contractID, supplierTenantID).First(&contract).Error; err != nil {
			return errors.New("travel contract not found")
		}
		if input.TravelTenantID != contract.TravelTenantID || strings.TrimSpace(input.ContractNo) != contract.ContractNo {
			return errors.New("travel agency and contract number cannot be changed")
		}
		_, priceJSON, err := normalizeTeamPriceRulesTx(tx, supplierTenantID, input.PriceRules)
		if err != nil {
			return err
		}
		before, _ := json.Marshal(contract)
		status := input.Status
		if status == "" {
			status = contract.Status
		}
		if err := tx.Model(&contract).Updates(map[string]interface{}{
			"status": status, "settlement_days": input.SettlementDays, "credit_limit_cents": input.CreditLimitCents,
			"price_rules_json": priceJSON, "starts_at": input.StartsAt, "ends_at": input.EndsAt,
		}).Error; err != nil {
			return err
		}
		after, _ := json.Marshal(input)
		return recordAuditTx(tx, operatorID, supplierTenantID, "admin", "tenant", "travel_contract.update", "travel_contract", contract.ID, "供应商调整旅行社合同", string(before), string(after))
	})
	if err != nil {
		return nil, err
	}
	return s.GetContract(supplierTenantID, contractID)
}

func decodeTeamPriceRules(raw string) ([]TeamPriceRule, error) {
	if strings.TrimSpace(raw) == "" {
		return []TeamPriceRule{}, nil
	}
	var rules []TeamPriceRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func (s *TeamService) contractView(contract model.TravelContract) (*TravelContractView, error) {
	rules, err := decodeTeamPriceRules(contract.PriceRulesJSON)
	if err != nil {
		return nil, err
	}
	var travel, supplier model.Tenant
	if err := model.DB.Select("id", "name").First(&travel, contract.TravelTenantID).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Select("id", "name").First(&supplier, contract.SupplierTenantID).Error; err != nil {
		return nil, err
	}
	for i := range rules {
		var product model.Product
		if err := model.DB.Select("id", "name", "scenic_area_id").Where("id = ? AND tenant_id = ?", rules[i].ProductID, contract.SupplierTenantID).First(&product).Error; err == nil {
			rules[i].ProductName = product.Name
			rules[i].ScenicAreaID = product.ScenicAreaID
			var area model.ScenicArea
			if err := model.DB.Select("id", "name").Where("id = ? AND tenant_id = ?", product.ScenicAreaID, contract.SupplierTenantID).First(&area).Error; err == nil {
				rules[i].ScenicAreaName = area.Name
			}
		}
	}
	return &TravelContractView{TravelContract: contract, TravelTenantName: travel.Name, SupplierTenantName: supplier.Name, PriceRules: rules}, nil
}

func (s *TeamService) GetContract(tenantID, contractID uint) (*TravelContractView, error) {
	var contract model.TravelContract
	if err := model.DB.Where("id = ? AND (travel_tenant_id = ? OR supplier_tenant_id = ?)", contractID, tenantID, tenantID).First(&contract).Error; err != nil {
		return nil, err
	}
	return s.contractView(contract)
}

func (s *TeamService) ListContracts(tenantID uint) ([]TravelContractView, error) {
	var contracts []model.TravelContract
	if err := model.DB.Where("travel_tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID).Order("created_at DESC").Find(&contracts).Error; err != nil {
		return nil, err
	}
	rows := make([]TravelContractView, 0, len(contracts))
	for _, contract := range contracts {
		view, err := s.contractView(contract)
		if err != nil {
			return nil, err
		}
		rows = append(rows, *view)
	}
	return rows, nil
}

func (s *TeamService) ListContractPartners(supplierTenantID uint) ([]TravelContractPartner, error) {
	if err := requireActiveTenantCapability(model.DB, supplierTenantID, "supplier"); err != nil {
		return nil, err
	}
	var relationships []model.DistributorRelationship
	if err := model.DB.Preload("AgentTenant").Where("supplier_tenant_id = ? AND travel_status = ?", supplierTenantID, "active").Order("created_at DESC").Find(&relationships).Error; err != nil {
		return nil, err
	}
	rows := make([]TravelContractPartner, 0, len(relationships))
	for _, relationship := range relationships {
		if err := requireActiveTenantCapability(model.DB, relationship.AgentTenantID, "travel_agency"); err != nil {
			continue
		}
		rows = append(rows, TravelContractPartner{TenantID: relationship.AgentTenantID, Name: relationship.AgentTenant.Name, SystemCode: relationship.AgentTenant.SystemCode, RelationshipID: relationship.ID})
	}
	return rows, nil
}

func normalizeTeamReferenceText(value, field string, maxRunes int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return "", fmt.Errorf("%s is too long", field)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", fmt.Errorf("%s contains invalid characters", field)
		}
	}
	return value, nil
}

func normalizeTeamAgentInput(input TeamAgentInput) (TeamAgentInput, error) {
	var err error
	if input.Name, err = normalizeTeamReferenceText(input.Name, "agent name", 80, true); err != nil {
		return input, err
	}
	if input.Phone, err = normalizeTeamReferenceText(input.Phone, "agent phone", 30, false); err != nil {
		return input, err
	}
	if input.JobNumber, err = normalizeTeamReferenceText(input.JobNumber, "agent job number", 50, true); err != nil {
		return input, err
	}
	return input, nil
}

func normalizeTeamGuideInput(input TeamGuideInput) (TeamGuideInput, error) {
	var err error
	if input.Name, err = normalizeTeamReferenceText(input.Name, "guide name", 80, true); err != nil {
		return input, err
	}
	if input.Phone, err = normalizeTeamReferenceText(input.Phone, "guide phone", 30, false); err != nil {
		return input, err
	}
	if input.LicenseNo, err = normalizeTeamReferenceText(input.LicenseNo, "guide license number", 80, false); err != nil {
		return input, err
	}
	return input, nil
}

func normalizeTeamVehicleInput(input TeamVehicleInput) (TeamVehicleInput, error) {
	var err error
	if input.PlateNumber, err = normalizeTeamReferenceText(input.PlateNumber, "plate number", 30, true); err != nil {
		return input, err
	}
	input.PlateNumber = strings.ToUpper(input.PlateNumber)
	if input.DriverName, err = normalizeTeamReferenceText(input.DriverName, "driver name", 80, false); err != nil {
		return input, err
	}
	if input.DriverPhone, err = normalizeTeamReferenceText(input.DriverPhone, "driver phone", 30, false); err != nil {
		return input, err
	}
	if input.Capacity < 0 {
		return input, errors.New("vehicle capacity cannot be negative")
	}
	return input, nil
}

func requireTeamReferenceActorTx(tx *gorm.DB, tenantID, actorUserID uint) error {
	if actorUserID == 0 {
		return errors.New("team operator is required")
	}
	var count int64
	if err := tx.Model(&model.User{}).Where("id = ? AND tenant_id = ?", actorUserID, tenantID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("team operator not found")
	}
	return nil
}

func teamReferenceJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func validateTeamReferenceStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status != "active" && status != "inactive" {
		return "", errors.New("reference status must be active or inactive")
	}
	return status, nil
}

func (s *TeamService) CreateAgent(tenantID, actorUserID uint, input TeamAgentInput) (*model.TravelAgent, error) {
	input, err := normalizeTeamAgentInput(input)
	if err != nil {
		return nil, err
	}
	agent := model.TravelAgent{TenantID: tenantID, Name: input.Name, Phone: input.Phone, JobNumber: input.JobNumber, Status: "active"}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Create(&agent).Error; err != nil {
			return err
		}
		after, err := teamReferenceJSON(&agent)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.agent.create", "travel_agent", agent.ID, "create team agent", "{}", after)
	})
	return &agent, err
}

func (s *TeamService) UpdateAgent(tenantID, agentID, actorUserID uint, input TeamAgentInput) (*model.TravelAgent, error) {
	input, err := normalizeTeamAgentInput(input)
	if err != nil {
		return nil, err
	}
	var agent model.TravelAgent
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", agentID, tenantID).First(&agent).Error; err != nil {
			return errors.New("team agent not found")
		}
		before, err := teamReferenceJSON(&agent)
		if err != nil {
			return err
		}
		if err := tx.Model(&agent).Updates(map[string]interface{}{"name": input.Name, "phone": input.Phone, "job_number": input.JobNumber}).Error; err != nil {
			return err
		}
		agent.Name, agent.Phone, agent.JobNumber = input.Name, input.Phone, input.JobNumber
		after, err := teamReferenceJSON(&agent)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.agent.update", "travel_agent", agent.ID, "update team agent", before, after)
	})
	return &agent, err
}

func (s *TeamService) SetAgentStatus(tenantID, agentID, actorUserID uint, status, reason string) (*model.TravelAgent, error) {
	status, err := validateTeamReferenceStatus(status)
	if err != nil {
		return nil, err
	}
	var agent model.TravelAgent
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", agentID, tenantID).First(&agent).Error; err != nil {
			return errors.New("team agent not found")
		}
		if agent.Status == status {
			return nil
		}
		before, _ := teamReferenceJSON(&agent)
		if err := tx.Model(&agent).Update("status", status).Error; err != nil {
			return err
		}
		agent.Status = status
		after, _ := teamReferenceJSON(&agent)
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.agent.status", "travel_agent", agent.ID, strings.TrimSpace(reason), before, after)
	})
	return &agent, err
}

func (s *TeamService) ListAgents(tenantID uint) ([]model.TravelAgent, error) {
	var rows []model.TravelAgent
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateGuide(tenantID, actorUserID uint, input TeamGuideInput) (*model.TourGuide, error) {
	input, err := normalizeTeamGuideInput(input)
	if err != nil {
		return nil, err
	}
	guide := model.TourGuide{TenantID: tenantID, Name: input.Name, Phone: input.Phone, LicenseNo: input.LicenseNo, Status: "active"}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Create(&guide).Error; err != nil {
			return err
		}
		after, err := teamReferenceJSON(&guide)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.guide.create", "tour_guide", guide.ID, "create tour guide", "{}", after)
	})
	return &guide, err
}

func (s *TeamService) UpdateGuide(tenantID, guideID, actorUserID uint, input TeamGuideInput) (*model.TourGuide, error) {
	input, err := normalizeTeamGuideInput(input)
	if err != nil {
		return nil, err
	}
	var guide model.TourGuide
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", guideID, tenantID).First(&guide).Error; err != nil {
			return errors.New("team guide not found")
		}
		before, err := teamReferenceJSON(&guide)
		if err != nil {
			return err
		}
		if err := tx.Model(&guide).Updates(map[string]interface{}{"name": input.Name, "phone": input.Phone, "license_no": input.LicenseNo}).Error; err != nil {
			return err
		}
		guide.Name, guide.Phone, guide.LicenseNo = input.Name, input.Phone, input.LicenseNo
		after, err := teamReferenceJSON(&guide)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.guide.update", "tour_guide", guide.ID, "update tour guide", before, after)
	})
	return &guide, err
}

func (s *TeamService) SetGuideStatus(tenantID, guideID, actorUserID uint, status, reason string) (*model.TourGuide, error) {
	status, err := validateTeamReferenceStatus(status)
	if err != nil {
		return nil, err
	}
	var guide model.TourGuide
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", guideID, tenantID).First(&guide).Error; err != nil {
			return errors.New("team guide not found")
		}
		if guide.Status == status {
			return nil
		}
		before, _ := teamReferenceJSON(&guide)
		if err := tx.Model(&guide).Update("status", status).Error; err != nil {
			return err
		}
		guide.Status = status
		after, _ := teamReferenceJSON(&guide)
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.guide.status", "tour_guide", guide.ID, strings.TrimSpace(reason), before, after)
	})
	return &guide, err
}

func (s *TeamService) ListGuides(tenantID uint) ([]model.TourGuide, error) {
	var rows []model.TourGuide
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateVehicle(tenantID, actorUserID uint, input TeamVehicleInput) (*model.TravelVehicle, error) {
	input, err := normalizeTeamVehicleInput(input)
	if err != nil {
		return nil, err
	}
	vehicle := model.TravelVehicle{TenantID: tenantID, PlateNumber: input.PlateNumber, DriverName: input.DriverName, DriverPhone: input.DriverPhone, Capacity: input.Capacity, Status: "active"}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Create(&vehicle).Error; err != nil {
			return err
		}
		after, err := teamReferenceJSON(&vehicle)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.vehicle.create", "travel_vehicle", vehicle.ID, "create travel vehicle", "{}", after)
	})
	return &vehicle, err
}

func (s *TeamService) UpdateVehicle(tenantID, vehicleID, actorUserID uint, input TeamVehicleInput) (*model.TravelVehicle, error) {
	input, err := normalizeTeamVehicleInput(input)
	if err != nil {
		return nil, err
	}
	var vehicle model.TravelVehicle
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", vehicleID, tenantID).First(&vehicle).Error; err != nil {
			return errors.New("team vehicle not found")
		}
		before, err := teamReferenceJSON(&vehicle)
		if err != nil {
			return err
		}
		if err := tx.Model(&vehicle).Updates(map[string]interface{}{"plate_number": input.PlateNumber, "driver_name": input.DriverName, "driver_phone": input.DriverPhone, "capacity": input.Capacity}).Error; err != nil {
			return err
		}
		vehicle.PlateNumber, vehicle.DriverName, vehicle.DriverPhone, vehicle.Capacity = input.PlateNumber, input.DriverName, input.DriverPhone, input.Capacity
		after, err := teamReferenceJSON(&vehicle)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.vehicle.update", "travel_vehicle", vehicle.ID, "update travel vehicle", before, after)
	})
	return &vehicle, err
}

func (s *TeamService) SetVehicleStatus(tenantID, vehicleID, actorUserID uint, status, reason string) (*model.TravelVehicle, error) {
	status, err := validateTeamReferenceStatus(status)
	if err != nil {
		return nil, err
	}
	var vehicle model.TravelVehicle
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireTeamReferenceActorTx(tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", vehicleID, tenantID).First(&vehicle).Error; err != nil {
			return errors.New("team vehicle not found")
		}
		if vehicle.Status == status {
			return nil
		}
		before, _ := teamReferenceJSON(&vehicle)
		if err := tx.Model(&vehicle).Update("status", status).Error; err != nil {
			return err
		}
		vehicle.Status = status
		after, _ := teamReferenceJSON(&vehicle)
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.vehicle.status", "travel_vehicle", vehicle.ID, strings.TrimSpace(reason), before, after)
	})
	return &vehicle, err
}

func (s *TeamService) ListVehicles(tenantID uint) ([]model.TravelVehicle, error) {
	var rows []model.TravelVehicle
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func teamNo(prefix string, id uint) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), id)
}

func (s *TeamService) CreateGroup(tenantID uint, group *model.TourGroup) error {
	if tenantID == 0 || strings.TrimSpace(group.Name) == "" || group.SupplierTenantID == 0 || group.ScenicAreaID == 0 || group.VisitDate.IsZero() {
		return errors.New("group name, supplier, scenic area and visit date are required")
	}
	if group.ExpectedCount < 1 {
		return errors.New("团队计划人数至少为 1")
	}
	if group.DepositCents != 0 {
		return errors.New("team deposit must be recorded through a verified payment workflow")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var area model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", group.ScenicAreaID, group.SupplierTenantID, "active").First(&area).Error; err != nil {
			return errors.New("supplier scenic area not found")
		}
		for _, assignment := range []struct {
			kind string
			id   uint
		}{
			{kind: "agent", id: group.AgentID},
			{kind: "guide", id: group.GuideID},
			{kind: "vehicle", id: group.VehicleID},
		} {
			if err := validateTeamPlanAssignmentTx(tx, tenantID, assignment.kind, assignment.id); err != nil {
				return err
			}
		}
		if err := validateTeamContractTx(tx, tenantID, group); err != nil {
			return err
		}
		group.Base = model.Base{}
		group.TenantID = tenantID
		group.SalesOrderID = 0
		group.ContractAmountCents = 0
		group.CreditUsedCents = 0
		group.SettlementStatus = "open"
		group.Status = "draft"
		group.GroupNo = teamNo("TEAM", tenantID)
		return tx.Create(group).Error
	})
}

func teamGroupPlanSnapshot(group *model.TourGroup) teamGroupPlanAuditSnapshot {
	return teamGroupPlanAuditSnapshot{
		Name: group.Name, VisitDate: group.VisitDate, GuideID: group.GuideID,
		VehicleID: group.VehicleID, AgentID: group.AgentID, Status: group.Status,
		SalesOrderID: group.SalesOrderID,
	}
}

func validateTeamPlanAssignmentTx(tx *gorm.DB, tenantID uint, kind string, id uint) error {
	if id == 0 {
		return nil
	}
	switch kind {
	case "guide":
		var count int64
		if err := tx.Model(&model.TourGuide{}).Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, "active").Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("所选导游不存在、已停用或不属于当前旅行社")
		}
	case "vehicle":
		var count int64
		if err := tx.Model(&model.TravelVehicle{}).Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, "active").Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("所选车辆不存在、已停用或不属于当前旅行社")
		}
	case "agent":
		var count int64
		if err := tx.Model(&model.TravelAgent{}).Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, "active").Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("所选业务员不存在、已停用或不属于当前旅行社")
		}
	default:
		return errors.New("未知团队安排类型")
	}
	return nil
}

// UpdateGroupPlan updates only the operational plan. Once a confirmation or
// order exists, fields that would rewrite its historical meaning are frozen;
// guide, vehicle and agent assignments may still be corrected with a reason
// until the group has fully entered.
func (s *TeamService) UpdateGroupPlan(tenantID, groupID, actorUserID uint, input TeamGroupPlanUpdate) (*model.TourGroup, error) {
	var result model.TourGroup
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("团队计划不存在")
		}
		if group.Status == "entered" {
			return errors.New("团队已全部入园，不能再修改计划")
		}
		if group.Status == "cancelled" {
			return errors.New("团队计划已取消，不能再修改")
		}
		if group.Status != "draft" && group.Status != "confirmed" && group.Status != "partial_entry" {
			return errors.New("当前团队状态不允许修改计划")
		}

		var confirmationCount int64
		if err := tx.Model(&model.TourGroupConfirmation{}).Where("group_id = ?", group.ID).Count(&confirmationCount).Error; err != nil {
			return err
		}
		restricted := group.SalesOrderID != 0 || group.Status != "draft" || confirmationCount > 0
		updates := make(map[string]interface{})
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" {
				return errors.New("团队名称不能为空")
			}
			if len([]rune(name)) > 120 {
				return errors.New("团队名称不能超过120个字符")
			}
			if name != group.Name {
				if confirmationCount > 0 {
					return errors.New("已有确认单的团队不能修改团队名称，请追加新的确认说明")
				}
				updates["name"] = name
			}
		}
		if input.VisitDate != nil {
			if input.VisitDate.IsZero() {
				return errors.New("到园日期不能为空")
			}
			visitDate := startOfDay(*input.VisitDate)
			if !sameTeamDate(visitDate, group.VisitDate) {
				if restricted {
					return errors.New("已有确认单、订单或履约进度的团队不能修改到园日期")
				}
				candidate := group
				candidate.VisitDate = visitDate
				if err := validateTeamContractTx(tx, tenantID, &candidate); err != nil {
					return err
				}
				updates["visit_date"] = visitDate
			}
		}
		if input.GuideID != nil && *input.GuideID != group.GuideID {
			if err := validateTeamPlanAssignmentTx(tx, tenantID, "guide", *input.GuideID); err != nil {
				return err
			}
			updates["guide_id"] = *input.GuideID
		}
		if input.VehicleID != nil && *input.VehicleID != group.VehicleID {
			if err := validateTeamPlanAssignmentTx(tx, tenantID, "vehicle", *input.VehicleID); err != nil {
				return err
			}
			updates["vehicle_id"] = *input.VehicleID
		}
		if input.AgentID != nil && *input.AgentID != group.AgentID {
			if err := validateTeamPlanAssignmentTx(tx, tenantID, "agent", *input.AgentID); err != nil {
				return err
			}
			updates["agent_id"] = *input.AgentID
		}
		if len(updates) == 0 {
			result = group
			return nil
		}
		reason := strings.TrimSpace(input.Reason)
		if restricted && reason == "" {
			return errors.New("已有确认单、订单或履约进度的团队修改计划时必须填写原因")
		}
		if len([]rune(reason)) > 255 {
			return errors.New("修改原因不能超过255个字符")
		}
		if reason == "" {
			reason = "维护草稿团队计划"
		}
		beforeJSON, err := json.Marshal(teamGroupPlanSnapshot(&group))
		if err != nil {
			return err
		}
		if err := tx.Model(&group).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.First(&group, group.ID).Error; err != nil {
			return err
		}
		afterJSON, err := json.Marshal(teamGroupPlanSnapshot(&group))
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.plan.update", "tour_group", group.ID, reason, string(beforeJSON), string(afterJSON)); err != nil {
			return err
		}
		result = group
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelGroup cancels only an unfunded, unfulfilled plan. It never refunds,
// voids tickets, releases credit or rewrites admission/settlement facts; a
// bound order must first be handled through the existing after-sale workflow.
func (s *TeamService) CancelGroup(tenantID, groupID, actorUserID uint, reason string) (*model.TourGroup, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("取消团队计划必须填写原因")
	}
	if len([]rune(reason)) > 255 {
		return nil, errors.New("取消原因不能超过255个字符")
	}
	var result model.TourGroup
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("团队计划不存在")
		}
		if group.Status == "cancelled" {
			result = group
			return nil
		}
		if group.Status == "entered" || group.Status == "partial_entry" {
			return errors.New("团队已有入园事实，不能直接取消计划")
		}
		if group.SalesOrderID != 0 {
			return errors.New("团队已绑定订单，不能直接取消；请先通过订单售后完成退票或作废，取消计划不会自动处理资金")
		}
		if group.ContractAmountCents != 0 || group.DepositCents != 0 || group.CreditUsedCents != 0 || group.SettlementStatus != "open" {
			return errors.New("团队已有资金或结算事实，不能直接取消计划")
		}
		var fulfillmentFacts int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND (ticket_code != '' OR status IN ?)", group.ID, []string{"ticketed", "entered"}).Count(&fulfillmentFacts).Error; err != nil {
			return err
		}
		if fulfillmentFacts > 0 {
			return errors.New("团队已有票权或入园事实，不能直接取消计划")
		}
		if err := tx.Model(&model.TourEntryBatch{}).Where("group_id = ?", group.ID).Count(&fulfillmentFacts).Error; err != nil {
			return err
		}
		if fulfillmentFacts > 0 {
			return errors.New("团队已有分批入园记录，不能直接取消计划")
		}
		if err := tx.Model(&model.TeamSettlementStatement{}).Where("group_id = ?", group.ID).Count(&fulfillmentFacts).Error; err != nil {
			return err
		}
		if fulfillmentFacts > 0 {
			return errors.New("团队已有结算单，不能直接取消计划")
		}
		beforeJSON, err := json.Marshal(teamGroupPlanSnapshot(&group))
		if err != nil {
			return err
		}
		if err := tx.Model(&group).Update("status", "cancelled").Error; err != nil {
			return err
		}
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND status = ?", group.ID, "planned").Update("status", "cancelled").Error; err != nil {
			return err
		}
		group.Status = "cancelled"
		afterJSON, err := json.Marshal(teamGroupPlanSnapshot(&group))
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.plan.cancel", "tour_group", group.ID, reason, string(beforeJSON), string(afterJSON)); err != nil {
			return err
		}
		result = group
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *TeamService) ListGroups(tenantID uint, page, pageSize int) ([]model.TourGroup, int64, error) {
	return s.ListGroupsWithOptions(tenantID, TeamGroupListOptions{Page: page, PageSize: pageSize})
}

func (s *TeamService) ListGroupsWithOptions(tenantID uint, options TeamGroupListOptions) ([]model.TourGroup, int64, error) {
	if options.Page < 1 {
		options.Page = 1
	}
	if options.PageSize < 1 {
		options.PageSize = 20
	}
	if options.PageSize > 100 {
		options.PageSize = 100
	}
	query := model.DB.Model(&model.TourGroup{}).Where("tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID)
	if keyword := strings.TrimSpace(options.Keyword); keyword != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(keyword)
		pattern := "%" + escaped + "%"
		query = query.Where(`(
			tour_groups.group_no ILIKE ? ESCAPE E'\\' OR
			tour_groups.name ILIKE ? ESCAPE E'\\' OR
			EXISTS (
				SELECT 1 FROM orders
				WHERE orders.id = tour_groups.sales_order_id
				  AND orders.tenant_id = tour_groups.tenant_id
				  AND orders.deleted_at IS NULL
				  AND orders.order_no ILIKE ? ESCAPE E'\\'
			)
		)`, pattern, pattern, pattern)
	}
	if status := strings.ToLower(strings.TrimSpace(options.Status)); status != "" {
		query = query.Where("tour_groups.status = ?", status)
	}
	if options.VisitStart != nil {
		query = query.Where("tour_groups.visit_date >= ?", startOfDay(*options.VisitStart))
	}
	if options.VisitEnd != nil {
		query = query.Where("tour_groups.visit_date <= ?", startOfDay(*options.VisitEnd))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []model.TourGroup
	if err := query.
		Order("CASE WHEN tour_groups.visit_date >= CURRENT_DATE THEN 0 ELSE 1 END ASC").
		Order("CASE WHEN tour_groups.visit_date >= CURRENT_DATE THEN tour_groups.visit_date END ASC").
		Order("CASE WHEN tour_groups.visit_date < CURRENT_DATE THEN tour_groups.visit_date END DESC").
		Order("tour_groups.created_at DESC").
		Order("tour_groups.id DESC").
		Offset((options.Page - 1) * options.PageSize).
		Limit(options.PageSize).
		Find(&groups).Error; err != nil {
		return nil, 0, err
	}
	orderIDs := make([]uint, 0, len(groups))
	for i := range groups {
		if groups[i].SalesOrderID > 0 {
			orderIDs = append(orderIDs, groups[i].SalesOrderID)
		}
	}
	if len(orderIDs) > 0 {
		var orders []model.Order
		if err := model.DB.Select("id", "tenant_id", "order_no").Where("id IN ?", orderIDs).Find(&orders).Error; err != nil {
			return nil, 0, err
		}
		orderNoByID := make(map[uint]string, len(orders))
		orderTenantByID := make(map[uint]uint, len(orders))
		for i := range orders {
			orderNoByID[orders[i].ID] = orders[i].OrderNo
			orderTenantByID[orders[i].ID] = orders[i].TenantID
		}
		for i := range groups {
			if orderTenantByID[groups[i].SalesOrderID] == groups[i].TenantID {
				groups[i].SalesOrderNo = orderNoByID[groups[i].SalesOrderID]
			}
		}
	}
	return groups, total, nil
}

func (s *TeamService) AddMembers(tenantID, groupID uint, members []model.TourGroupMember) (int, error) {
	if len(members) == 0 {
		return 0, errors.New("members are required")
	}
	count := 0
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.Status != "draft" || group.SalesOrderID != 0 {
			return errors.New("members can only be added before the group is confirmed")
		}
		var existingCount int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ?", groupID).Count(&existingCount).Error; err != nil {
			return err
		}
		newCount := int(existingCount) + len(members)
		if group.ExpectedCount > 0 && newCount > group.ExpectedCount {
			return errors.New("团队名单不能超过计划人数")
		}
		for i := range members {
			if strings.TrimSpace(members[i].Name) == "" {
				return errors.New("member name is required")
			}
			identity := strings.TrimSpace(members[i].IdentityNo)
			if identity != "" {
				var duplicate int64
				if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND identity_no = ? AND identity_no != ''", groupID, identity).Count(&duplicate).Error; err != nil {
					return err
				}
				if duplicate > 0 {
					return fmt.Errorf("member identity %s already exists in group", identity)
				}
				for j := 0; j < i; j++ {
					if strings.TrimSpace(members[j].IdentityNo) == identity {
						return fmt.Errorf("member identity %s is duplicated in request", identity)
					}
				}
			}
			members[i].Base = model.Base{}
			members[i].GroupID = groupID
			members[i].Status = "planned"
			members[i].TicketCode = ""
			members[i].EnteredAt = nil
			members[i].EntryBatchNo = ""
			if err := tx.Create(&members[i]).Error; err != nil {
				return err
			}
			count++
		}
		if group.ExpectedCount <= 0 {
			return tx.Model(&group).UpdateColumn("expected_count", newCount).Error
		}
		return nil
	})
	return count, err
}

// ReplaceMembers is the safe roster import path. A roster can only be
// replaced before the group is confirmed; once tickets or admission facts
// exist, deleting rows would orphan entitlements and audit history.
func (s *TeamService) ReplaceMembers(tenantID, groupID uint, members []model.TourGroupMember) (int, error) {
	if len(members) == 0 {
		return 0, errors.New("团队名单不能为空")
	}
	returnCount := len(members)
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.Status != "draft" || group.SalesOrderID != 0 {
			return errors.New("roster can only be replaced before the group is confirmed")
		}
		seen := make(map[string]struct{})
		for i := range members {
			members[i].Name = strings.TrimSpace(members[i].Name)
			members[i].IdentityNo = strings.TrimSpace(members[i].IdentityNo)
			if members[i].Name == "" {
				return errors.New("member name is required")
			}
			if members[i].IdentityNo != "" {
				if _, exists := seen[members[i].IdentityNo]; exists {
					return fmt.Errorf("member identity %s is duplicated in request", members[i].IdentityNo)
				}
				seen[members[i].IdentityNo] = struct{}{}
			}
			members[i].Base = model.Base{}
			members[i].GroupID = groupID
			members[i].Status = "planned"
			members[i].TicketCode = ""
		}
		if err := tx.Where("group_id = ?", groupID).Delete(&model.TourGroupMember{}).Error; err != nil {
			return err
		}
		if len(members) > 0 {
			if err := tx.Create(&members).Error; err != nil {
				return err
			}
		}
		return tx.Model(&group).Updates(map[string]interface{}{"expected_count": len(members), "status": "draft"}).Error
	})
	return returnCount, err
}

func (s *TeamService) ListMembers(tenantID, groupID uint) ([]model.TourGroupMember, error) {
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND (tenant_id = ? OR supplier_tenant_id = ?)", groupID, tenantID, tenantID).First(&group).Error; err != nil {
		return nil, errors.New("group not found")
	}
	var members []model.TourGroupMember
	return members, model.DB.Where("group_id = ?", groupID).Order("id ASC").Find(&members).Error
}

func (s *TeamService) EnterBatch(tenantID, groupID, deviceID, operatorID uint, memberIDs []uint, idempotencyKeys ...string) (*model.TourEntryBatch, error) {
	if len(memberIDs) == 0 || deviceID == 0 || operatorID == 0 {
		return nil, errors.New("member ids, supplier device and operator are required")
	}
	idempotencyKey := ""
	if len(idempotencyKeys) > 0 {
		idempotencyKey = strings.TrimSpace(idempotencyKeys[0])
	}
	if idempotencyKey == "" {
		return nil, errors.New("entry idempotency key is required")
	}
	if len(idempotencyKey) > 120 {
		return nil, errors.New("entry idempotency key is too long")
	}
	normalizedMemberIDs := append([]uint(nil), memberIDs...)
	sort.Slice(normalizedMemberIDs, func(i, j int) bool { return normalizedMemberIDs[i] < normalizedMemberIDs[j] })
	for i, memberID := range normalizedMemberIDs {
		if memberID == 0 || i > 0 && normalizedMemberIDs[i-1] == memberID {
			return nil, errors.New("entry member ids must be unique and non-zero")
		}
	}
	memberIDsJSON, err := json.Marshal(normalizedMemberIDs)
	if err != nil {
		return nil, err
	}
	var batch model.TourEntryBatch
	err = model.Write(func(tx *gorm.DB) error {
		var group model.TourGroup
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND supplier_tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		var existing model.TourEntryBatch
		if err := tx.Where("group_id = ? AND idempotency_key = ?", groupID, idempotencyKey).First(&existing).Error; err == nil {
			if existing.DeviceID != deviceID || existing.OperatorID != operatorID || existing.MemberIDsJSON != string(memberIDsJSON) {
				return errors.New("entry idempotency key was used with different data")
			}
			batch = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if group.SalesOrderID == 0 || (group.Status != "confirmed" && group.Status != "partial_entry") {
			return errors.New("group has no confirmed sales fulfillment")
		}
		var order model.Order
		if err := tx.Where("id = ? AND tenant_id = ? AND status IN ?", group.SalesOrderID, group.TenantID, []string{"paid", "completed", "partial_refunded"}).First(&order).Error; err != nil {
			return errors.New("group sales order is not paid")
		}
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND status = ? AND check_point_id IS NOT NULL", deviceID, group.SupplierTenantID, group.ScenicAreaID, "online").First(&device).Error; err != nil {
			return errors.New("entry device does not belong to group scenic area")
		}
		var operatorCount int64
		if err := tx.Model(&model.User{}).Where("id = ? AND tenant_id = ?", operatorID, group.SupplierTenantID).Count(&operatorCount).Error; err != nil {
			return err
		}
		if operatorCount == 0 {
			if err := tx.Model(&model.Staff{}).Where("id = ? AND tenant_id = ? AND status = ?", operatorID, group.SupplierTenantID, "active").Count(&operatorCount).Error; err != nil {
				return err
			}
		}
		if operatorCount == 0 {
			return errors.New("entry operator does not belong to supplier tenant")
		}
		checkpointID := *device.CheckPointID
		batch = model.TourEntryBatch{
			GroupID: groupID, SupplierTenantID: group.SupplierTenantID, ScenicAreaID: group.ScenicAreaID,
			BatchNo: teamNo("BATCH", groupID), IdempotencyKey: idempotencyKey, MemberIDsJSON: string(memberIDsJSON),
			DeviceID: deviceID, EnteredCount: 0, OperatorID: operatorID, EnteredAt: time.Now(),
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, memberID := range normalizedMemberIDs {
			var member model.TourGroupMember
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND group_id = ? AND status = ?", memberID, groupID, "ticketed").First(&member).Error; err != nil {
				return fmt.Errorf("member %d is not ticketed", memberID)
			}
			var ticket model.Ticket
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("OrderItem").Where("ticket_code = ? AND order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status IN ?", member.TicketCode, order.ID, group.SupplierTenantID, group.ScenicAreaID, []string{"unused", "active"}).First(&ticket).Error; err != nil {
				return fmt.Errorf("member %d has no valid ticket entitlement", memberID)
			}
			if ticket.OrderItem.UseDate == nil || !sameTeamDate(*ticket.OrderItem.UseDate, group.VisitDate) {
				return fmt.Errorf("member %d ticket visit date does not match team", memberID)
			}
			if ticket.Environment == "sandbox" || ticket.PendingRefundID != 0 {
				return fmt.Errorf("member %d ticket entitlement is unavailable", memberID)
			}
			if ticket.OrderItem.ValidityStart != nil && now.Before(*ticket.OrderItem.ValidityStart) {
				return fmt.Errorf("member %d ticket entitlement is not valid yet", memberID)
			}
			if ticket.OrderItem.ValidityEnd != nil && now.After(*ticket.OrderItem.ValidityEnd) {
				return fmt.Errorf("member %d ticket entitlement has expired", memberID)
			}
			if ticket.CheckInCount != 0 {
				return fmt.Errorf("member %d ticket entitlement was already admitted", memberID)
			}
			if ticket.CodeMode == "order" {
				return errors.New("team admission requires one ticket entitlement per member")
			}
			var rule model.TicketRule
			if ticket.RuleSnapshot == "" || json.Unmarshal([]byte(ticket.RuleSnapshot), &rule) != nil {
				return errors.New("ticket entitlement has no valid rule snapshot")
			}
			groupMatch, itemMatch := matchRule(rule, checkpointID)
			if groupMatch == nil || itemMatch == nil {
				return errors.New("entry checkpoint is not allowed by ticket entitlement")
			}
			var records []model.CheckInRecord
			if err := tx.Where("ticket_id = ? AND result = ?", ticket.ID, "success").Find(&records).Error; err != nil {
				return err
			}
			product := model.Product{CodeMode: ticket.CodeMode, Rule: rule}
			limit := admissionLimit(&product, &ticket.OrderItem, itemMatch)
			if limit <= 0 || countAtCheckpoint(records, checkpointID) >= limit {
				return errors.New("ticket checkpoint admission limit reached")
			}
			if !groupAllowsCheckpoint(records, groupMatch, checkpointID) {
				return errors.New("ticket benefit group admission limit reached")
			}
			if err := tx.Create(&model.CheckInRecord{TenantID: group.SupplierTenantID, ScenicAreaID: group.ScenicAreaID, TicketCode: ticket.TicketCode, TicketID: ticket.ID, CheckPointID: checkpointID, DeviceID: device.ID, CheckInTime: now, Result: "success", Message: "team admission"}).Error; err != nil {
				return err
			}
			result := tx.Model(&model.Ticket{}).Where("id = ? AND status IN ? AND check_in_count = 0", ticket.ID, []string{"unused", "active"}).Updates(map[string]interface{}{"status": "used", "check_in_count": gorm.Expr("check_in_count + 1")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("member %d ticket entitlement was already admitted", memberID)
			}
			if err := tx.Model(&model.TourGroupMember{}).Where("id = ? AND group_id = ? AND status = ?", member.ID, groupID, "ticketed").Updates(map[string]interface{}{"status": "entered", "entered_at": now, "entry_batch_no": batch.BatchNo}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ? AND fulfillment_order_id = ?", ticket.ID, ticket.FulfillmentOrderID).Update("status", "used").Error; err != nil {
				return err
			}
			batch.EnteredCount++
		}
		if err := tx.Model(&batch).Update("entered_count", batch.EnteredCount).Error; err != nil {
			return err
		}
		var totalEntered int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND status = ?", group.ID, "entered").Count(&totalEntered).Error; err != nil {
			return err
		}
		status := "partial_entry"
		if int(totalEntered) >= group.ExpectedCount && group.ExpectedCount > 0 {
			status = "entered"
		}
		if err := tx.Model(&group).Update("status", status).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "seller", "tenant", "team.entry_batch", "tour_entry_batch", batch.ID,
			fmt.Sprintf("team %s admitted %d members", group.GroupNo, batch.EnteredCount), "", fmt.Sprintf(`{"group_id":%d,"batch_no":%q,"entered_count":%d}`, group.ID, batch.BatchNo, batch.EnteredCount))
	})
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

func (s *TeamService) ListEntryBatches(tenantID, groupID uint) ([]model.TourEntryBatch, error) {
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND (tenant_id = ? OR supplier_tenant_id = ?)", groupID, tenantID, tenantID).First(&group).Error; err != nil {
		return nil, errors.New("group not found")
	}
	var batches []model.TourEntryBatch
	return batches, model.DB.Where("group_id = ?", groupID).Order("entered_at DESC, id DESC").Find(&batches).Error
}

func (s *TeamService) AttachOrder(tenantID, groupID, orderID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.Status != "draft" || group.SalesOrderID != 0 {
			return errors.New("only an unbound draft team can attach a sales order")
		}
		var order model.Order
		if err := tx.Preload("Items").Where("id = ? AND tenant_id = ? AND status IN ?", orderID, tenantID, []string{"paid", "completed", "partial_refunded"}).First(&order).Error; err != nil {
			return errors.New("sales order not found")
		}
		if err := validateTeamContractTx(tx, tenantID, &group); err != nil {
			return err
		}
		if group.ContractID != 0 {
			var contract model.TravelContract
			if err := tx.Where("id = ? AND travel_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", group.ContractID, tenantID, group.SupplierTenantID, "active").First(&contract).Error; err != nil {
				return errors.New("active travel contract not found")
			}
			if err := validateTeamOrderAgainstContract(&contract, &order); err != nil {
				return err
			}
		}
		for _, item := range order.Items {
			if item.FulfillmentTenantID != group.SupplierTenantID || item.FulfillmentScenicAreaID != group.ScenicAreaID {
				continue
			}
			if item.UseDate == nil || !sameTeamDate(*item.UseDate, group.VisitDate) {
				return errors.New("sales order visit date does not match team visit date")
			}
		}
		var count int64
		if err := tx.Model(&model.FulfillmentOrder{}).Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", order.ID, group.SupplierTenantID, group.ScenicAreaID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("order has no matching supplier fulfillment")
		}
		var existingGroupCount int64
		if err := tx.Model(&model.TourGroup{}).
			Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ? AND id != ? AND status != ?", order.ID, group.SupplierTenantID, group.ScenicAreaID, group.ID, "cancelled").
			Count(&existingGroupCount).Error; err != nil {
			return err
		}
		if existingGroupCount > 0 {
			return errors.New("this supplier fulfillment is already attached to another active team")
		}
		var members []model.TourGroupMember
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ? AND status = ?", group.ID, "planned").Order("id").Find(&members).Error; err != nil {
			return err
		}
		if len(members) == 0 || len(members) != group.ExpectedCount {
			return errors.New("team roster must match the planned headcount before attaching an order")
		}
		var tickets []model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(`order_id = ? AND tenant_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status = ? AND code_mode != ?
			AND NOT EXISTS (
				SELECT 1 FROM tour_group_members AS assigned_member
				JOIN tour_groups AS assigned_group ON assigned_group.id = assigned_member.group_id AND assigned_group.deleted_at IS NULL
				WHERE assigned_member.deleted_at IS NULL AND assigned_member.ticket_code = tickets.ticket_code
				  AND assigned_member.group_id != ? AND assigned_member.status != ? AND assigned_group.status != ?
			)`, order.ID, order.TenantID, group.SupplierTenantID, group.ScenicAreaID, "unused", "order", group.ID, "cancelled", "cancelled").Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		if len(tickets) < len(members) {
			return errors.New("order does not have enough member ticket entitlements")
		}
		assignedTickets := tickets[:len(members)]
		for i := range members {
			if err := tx.Model(&members[i]).Updates(map[string]interface{}{"ticket_code": assignedTickets[i].TicketCode, "status": "ticketed"}).Error; err != nil {
				return err
			}
		}
		amountCents, err := teamAssignedTicketSettlementCents(&order, assignedTickets, group.SupplierTenantID, group.ScenicAreaID)
		if err != nil {
			return err
		}
		if amountCents <= 0 {
			return errors.New("order has no settlement amount for the team supplier and scenic area")
		}
		// An existing paid order has already completed its original funding path.
		// Treat its supplier settlement amount as funded instead of occupying the
		// travel contract credit a second time.
		return tx.Model(&group).Updates(map[string]interface{}{
			"sales_order_id": order.ID, "status": "confirmed", "contract_amount_cents": amountCents,
			"deposit_cents": amountCents, "credit_used_cents": 0, "settlement_status": "open",
		}).Error
	})
}

// CreateContractOrder creates the paid fulfillment fact for a pure travel-agency
// team. Product ownership, visit date and price are derived from the active
// supplier contract; the client cannot submit distribution or settlement fields.
func (s *TeamService) CreateContractOrder(tenantID, groupID, operatorID uint, input TeamOrderInput) (*model.Order, error) {
	if input.ProductID == 0 {
		return nil, errors.New("请选择合同产品")
	}
	var order model.Order
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("团队不存在")
		}
		if group.Status != "draft" || group.SalesOrderID != 0 {
			return errors.New("只有尚未绑定订单的草稿团队可以生成合同订单")
		}
		if err := validateTeamContractTx(tx, tenantID, &group); err != nil {
			return err
		}
		var contract model.TravelContract
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND travel_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", group.ContractID, tenantID, group.SupplierTenantID, "active").First(&contract).Error; err != nil {
			return errors.New("有效旅行社合同不存在")
		}
		rules, err := decodeTeamPriceRules(contract.PriceRulesJSON)
		if err != nil {
			return errors.New("合同产品价格配置无效")
		}
		var priceRule *TeamPriceRule
		for i := range rules {
			if rules[i].ProductID == input.ProductID {
				priceRule = &rules[i]
				break
			}
		}
		if priceRule == nil || priceRule.PriceCents <= 0 {
			return errors.New("所选产品不在当前合同中")
		}
		var members []model.TourGroupMember
		if err := tx.Where("group_id = ? AND status = ?", group.ID, "planned").Order("id ASC").Find(&members).Error; err != nil {
			return err
		}
		quantity := len(members)
		if quantity == 0 || quantity != group.ExpectedCount {
			return errors.New("请先录入与计划人数一致的游客名单")
		}
		if priceRule.MaxQuantity > 0 && quantity > priceRule.MaxQuantity {
			return errors.New("团队人数超过合同产品每单上限")
		}
		var product model.Product
		if err := eligibleTeamContractProductsTx(tx, group.SupplierTenantID).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").
			Where("products.id = ? AND products.scenic_area_id = ?", input.ProductID, group.ScenicAreaID).
			First(&product).Error; err != nil {
			return errors.New("合同产品当前不可售或不属于团队景区")
		}
		if err := requireActiveTenantCapability(tx, product.TenantID, "supplier"); err != nil {
			return errors.New("景区供应商当前不可用")
		}
		revision, err := ensureProductRevisionTx(tx, &product)
		if err != nil {
			return err
		}
		product.CurrentRevisionID = revision.ID
		product.GateVoiceCode = strings.TrimSpace(revision.GateVoiceCode)
		if product.GateVoiceCode == "" {
			product.GateVoiceCode = "welcome"
		}
		visitors := make([]model.VisitorInput, quantity)
		for i := range members {
			visitors[i] = model.VisitorInput{Name: members[i].Name, Phone: members[i].Phone, IdentityNo: members[i].IdentityNo}
		}
		useDate := startOfDay(group.VisitDate)
		orderService := &OrderService{}
		order = model.Order{
			OrderNo: orderService.GenerateOrderNo(), TenantID: tenantID, Status: "unpaid", Channel: "team",
			ContactName: strings.TrimSpace(input.ContactName), ContactPhone: strings.TrimSpace(input.ContactPhone),
			TotalAmount: centsMoney(priceRule.PriceCents * int64(quantity)),
			Items: []model.OrderItem{{
				ProductID: product.ID, ProductName: product.Name, Price: centsMoney(priceRule.PriceCents),
				SettlementPrice: centsMoney(priceRule.PriceCents), Quantity: quantity, UseDate: &useDate,
				ValidityType: product.ValidityType, FulfillmentProductID: product.ID,
				FulfillmentTenantID: product.TenantID, FulfillmentScenicAreaID: product.ScenicAreaID,
				ProductRevisionID: revision.ID, Visitors: visitors,
			}},
		}
		item := &order.Items[0]
		if err := applyValidity(item, &product); err != nil {
			return err
		}
		if err := validateSalePolicyTx(tx, &product, &order, item, newSalePolicyContext()); err != nil {
			return err
		}
		if err := reserveStock(tx, &product, item.UseDate, item.StockSlot, quantity); err != nil {
			return err
		}
		item.Tickets, err = buildTickets(orderService, &product, quantity, &order)
		if err != nil {
			return err
		}
		if err := assignTicketVisitors(item); err != nil {
			return err
		}
		creditUsed := priceRule.PriceCents*int64(quantity) - group.DepositCents
		if creditUsed < 0 {
			creditUsed = 0
		}
		var occupied int64
		if err := tx.Model(&model.TourGroup{}).Where("contract_id = ? AND id != ? AND status != ? AND settlement_status != ?", contract.ID, group.ID, "cancelled", "settled").Select("COALESCE(SUM(credit_used_cents), 0)").Scan(&occupied).Error; err != nil {
			return err
		}
		if occupied+creditUsed > contract.CreditLimitCents {
			return errors.New("团队订单超过合同可用授信额度")
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE tickets SET order_id = ? WHERE order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", order.ID, order.ID).Error; err != nil {
			return err
		}
		if err := persistOrderVisitorsTx(tx, &order); err != nil {
			return err
		}
		if err := createFulfillmentProjections(tx, orderService, &order); err != nil {
			return err
		}
		now := time.Now()
		payment := model.Payment{
			TenantID: tenantID, PaymentNo: generatePaymentNo(), IdempotencyKey: fmt.Sprintf("team-order:%d", group.ID),
			OrderNo: order.OrderNo, Purpose: "order", Amount: order.TotalAmount,
			AmountCents: moneyCents(order.TotalAmount), Method: "team_account", Status: "paid",
			PaidAt: &now, TransactionID: fmt.Sprintf("TEAM_%d", group.ID), OperatorID: operatorID,
		}
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		if err := settleOrderIfFullyPaidTx(tx, &order); err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
			return err
		}
		for i := range members {
			if err := tx.Model(&members[i]).Updates(map[string]interface{}{"ticket_code": tickets[i].TicketCode, "status": "ticketed"}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&group).Updates(map[string]interface{}{
			"sales_order_id": order.ID, "status": "confirmed", "contract_amount_cents": moneyCents(order.TotalAmount),
			"credit_used_cents": creditUsed, "settlement_status": "open",
		}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "team.contract_order.create", "tour_group", group.ID,
			"旅行社按合同生成团队订单", "{}", fmt.Sprintf(`{"order_no":%q,"product_id":%d,"quantity":%d}`, order.OrderNo, product.ID, quantity))
	})
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func teamAssignedTicketSettlementCents(order *model.Order, tickets []model.Ticket, supplierTenantID, scenicAreaID uint) (int64, error) {
	if order == nil || order.ID == 0 || order.TenantID == 0 || len(tickets) == 0 {
		return 0, errors.New("assigned team tickets are required")
	}
	items := make(map[uint]model.OrderItem, len(order.Items))
	for i := range order.Items {
		item := order.Items[i]
		if item.OrderID == order.ID && item.FulfillmentTenantID == supplierTenantID && item.FulfillmentScenicAreaID == scenicAreaID {
			items[item.ID] = item
		}
	}
	var total int64
	for i := range tickets {
		ticket := tickets[i]
		if ticket.OrderID != order.ID || ticket.TenantID != order.TenantID ||
			ticket.FulfillmentTenantID != supplierTenantID || ticket.FulfillmentScenicAreaID != scenicAreaID ||
			ticket.CodeMode == "order" {
			return 0, errors.New("assigned team ticket ownership is invalid")
		}
		item, ok := items[ticket.OrderItemID]
		if !ok {
			return 0, errors.New("assigned team ticket settlement snapshot is unavailable")
		}
		total += moneyCents(item.SettlementPrice)
	}
	return total, nil
}

func sameTeamDate(left, right time.Time) bool {
	leftYear, leftMonth, leftDay := left.Date()
	rightYear, rightMonth, rightDay := right.Date()
	return leftYear == rightYear && leftMonth == rightMonth && leftDay == rightDay
}

func validateTeamContractTx(tx *gorm.DB, tenantID uint, group *model.TourGroup) error {
	if group == nil || group.SupplierTenantID == 0 || group.VisitDate.IsZero() {
		return errors.New("team supplier and visit date are required")
	}
	if err := requireActiveTenantCapability(tx, group.SupplierTenantID, "supplier"); err != nil {
		return errors.New("supplier tenant is unavailable")
	}
	if tenantID == group.SupplierTenantID {
		return nil
	}
	var relationship model.DistributorRelationship
	if err := tx.Where(
		"agent_tenant_id = ? AND supplier_tenant_id = ? AND travel_status = ?",
		tenantID, group.SupplierTenantID, "active",
	).First(&relationship).Error; err != nil {
		return errors.New("active travel agency relationship not found")
	}
	if group.ContractID == 0 {
		return errors.New("active travel contract is required")
	}
	var contract model.TravelContract
	if err := tx.Where("id = ? AND travel_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", group.ContractID, tenantID, group.SupplierTenantID, "active").First(&contract).Error; err != nil {
		return errors.New("active travel contract not found")
	}
	visit := startOfDay(group.VisitDate)
	if contract.StartsAt != nil && visit.Before(startOfDay(*contract.StartsAt)) {
		return errors.New("travel contract is not active on team visit date")
	}
	if contract.EndsAt != nil && visit.After(startOfDay(*contract.EndsAt)) {
		return errors.New("travel contract is not active on team visit date")
	}
	return nil
}

func validateTeamOrderAgainstContract(contract *model.TravelContract, order *model.Order) error {
	if contract == nil || order == nil {
		return errors.New("team contract and order are required")
	}
	raw := strings.TrimSpace(contract.PriceRulesJSON)
	if raw == "" {
		return nil
	}
	rules, err := decodeTeamPriceRules(raw)
	if err != nil {
		return fmt.Errorf("invalid team contract price rules: %w", err)
	}
	byProduct := make(map[uint]TeamPriceRule, len(rules))
	for _, rule := range rules {
		if rule.ProductID == 0 || rule.PriceCents <= 0 || rule.MaxQuantity < 0 {
			return errors.New("invalid team contract price rule")
		}
		byProduct[rule.ProductID] = rule
	}
	quantities := make(map[uint]int)
	for _, item := range order.Items {
		if item.FulfillmentTenantID != 0 && item.FulfillmentTenantID != contract.SupplierTenantID {
			continue
		}
		rule, ok := byProduct[item.FulfillmentProductID]
		if !ok {
			return fmt.Errorf("supplier product %d is not authorized by team contract", item.FulfillmentProductID)
		}
		if moneyCents(item.SettlementPrice) != rule.PriceCents {
			return fmt.Errorf("supplier product %d settlement price does not match team contract", item.FulfillmentProductID)
		}
		quantities[item.FulfillmentProductID] += item.Quantity
	}
	for productID, quantity := range quantities {
		if rule := byProduct[productID]; rule.MaxQuantity > 0 && quantity > rule.MaxQuantity {
			return fmt.Errorf("supplier product %d exceeds team contract quantity", productID)
		}
	}
	return nil
}
