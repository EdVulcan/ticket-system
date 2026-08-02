package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TeamService struct{}

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
		if err := tx.Select("id", "name", "scenic_area_id").Where("id = ? AND tenant_id = ? AND status = ? AND is_distributable = ?", rule.ProductID, supplierTenantID, "online", true).First(&product).Error; err != nil {
			return nil, "", fmt.Errorf("contract product %d is unavailable", rule.ProductID)
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

func syncTravelContractOffersTx(tx *gorm.DB, supplierTenantID, travelTenantID uint, rules []TeamPriceRule) error {
	if err := requireActiveTenantCapability(tx, travelTenantID, "distributor"); err != nil {
		return errors.New("该旅行社尚未启用分销能力")
	}
	for _, rule := range rules {
		var product model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", rule.ProductID, supplierTenantID).First(&product).Error; err != nil {
			return errors.New("合同产品当前不可用")
		}
		revision, err := ensureProductRevisionTx(tx, &product)
		if err != nil {
			return err
		}
		var offer model.ProductOffer
		err = tx.Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ?", supplierTenantID, travelTenantID, product.ID).First(&offer).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			offer = model.ProductOffer{
				SupplierTenantID: supplierTenantID, DistributorTenantID: travelTenantID,
				SourceProductID: product.ID, ProductRevisionID: revision.ID, FulfillmentScenicAreaID: product.ScenicAreaID,
				SettlementPrice: centsMoney(rule.PriceCents), MinimumRetailPriceCents: rule.PriceCents,
				Status: "active", AllowedChannels: "window,online,ota",
			}
			if err := tx.Create(&offer).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if offer.Status != "active" {
			return fmt.Errorf("产品“%s”的供货已暂停或终止，请先在分销管理中恢复", product.Name)
		}
		minimumRetailPriceCents := offer.MinimumRetailPriceCents
		if minimumRetailPriceCents < rule.PriceCents {
			minimumRetailPriceCents = rule.PriceCents
		}
		if err := tx.Model(&offer).Updates(map[string]interface{}{
			"product_revision_id": revision.ID, "fulfillment_scenic_area_id": product.ScenicAreaID,
			"settlement_price": centsMoney(rule.PriceCents), "minimum_retail_price_cents": minimumRetailPriceCents,
		}).Error; err != nil {
			return err
		}
	}
	return nil
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
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", input.TravelTenantID, supplierTenantID, "active").First(&relationship).Error; err != nil {
			return errors.New("active travel agency relationship not found")
		}
		normalizedRules, priceJSON, err := normalizeTeamPriceRulesTx(tx, supplierTenantID, input.PriceRules)
		if err != nil {
			return err
		}
		if err := syncTravelContractOffersTx(tx, supplierTenantID, input.TravelTenantID, normalizedRules); err != nil {
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
		normalizedRules, priceJSON, err := normalizeTeamPriceRulesTx(tx, supplierTenantID, input.PriceRules)
		if err != nil {
			return err
		}
		if err := syncTravelContractOffersTx(tx, supplierTenantID, input.TravelTenantID, normalizedRules); err != nil {
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
	if err := model.DB.Preload("AgentTenant").Where("supplier_tenant_id = ? AND status = ?", supplierTenantID, "active").Order("created_at DESC").Find(&relationships).Error; err != nil {
		return nil, err
	}
	rows := make([]TravelContractPartner, 0, len(relationships))
	for _, relationship := range relationships {
		if err := requireActiveTenantCapability(model.DB, relationship.AgentTenantID, "travel_agency"); err != nil {
			continue
		}
		if err := requireActiveTenantCapability(model.DB, relationship.AgentTenantID, "distributor"); err != nil {
			continue
		}
		rows = append(rows, TravelContractPartner{TenantID: relationship.AgentTenantID, Name: relationship.AgentTenant.Name, SystemCode: relationship.AgentTenant.SystemCode, RelationshipID: relationship.ID})
	}
	return rows, nil
}

func (s *TeamService) CreateAgent(tenantID uint, agent *model.TravelAgent) error {
	if strings.TrimSpace(agent.Name) == "" || strings.TrimSpace(agent.JobNumber) == "" {
		return errors.New("agent name and job number are required")
	}
	agent.Base = model.Base{}
	agent.TenantID = tenantID
	if agent.Status == "" {
		agent.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(agent).Error
	})
}

func (s *TeamService) ListAgents(tenantID uint) ([]model.TravelAgent, error) {
	var rows []model.TravelAgent
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateGuide(tenantID uint, guide *model.TourGuide) error {
	if strings.TrimSpace(guide.Name) == "" {
		return errors.New("guide name is required")
	}
	guide.Base = model.Base{}
	guide.TenantID = tenantID
	if guide.Status == "" {
		guide.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(guide).Error
	})
}

func (s *TeamService) ListGuides(tenantID uint) ([]model.TourGuide, error) {
	var rows []model.TourGuide
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateVehicle(tenantID uint, vehicle *model.TravelVehicle) error {
	if strings.TrimSpace(vehicle.PlateNumber) == "" {
		return errors.New("plate number is required")
	}
	vehicle.Base = model.Base{}
	vehicle.TenantID = tenantID
	if vehicle.Status == "" {
		vehicle.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(vehicle).Error
	})
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
	if group.DepositCents < 0 {
		return errors.New("team deposit cannot be negative")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var area model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", group.ScenicAreaID, group.SupplierTenantID, "active").First(&area).Error; err != nil {
			return errors.New("supplier scenic area not found")
		}
		if group.SupplierTenantID != tenantID {
			var relationship model.DistributorRelationship
			if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", tenantID, group.SupplierTenantID, "active").First(&relationship).Error; err != nil {
				return errors.New("active supplier relationship not found")
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

func (s *TeamService) ListGroups(tenantID uint, page, pageSize int) ([]model.TourGroup, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.TourGroup{}).Where("tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []model.TourGroup
	if err := query.Order("visit_date ASC, created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&groups).Error; err != nil {
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
		var group model.TourGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.Status == "entered" || group.Status == "cancelled" {
			return errors.New("cannot add members to a completed or cancelled group")
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
			if members[i].Status == "" {
				members[i].Status = "planned"
				if strings.TrimSpace(members[i].TicketCode) != "" {
					members[i].Status = "ticketed"
				}
			}
			if err := tx.Create(&members[i]).Error; err != nil {
				return err
			}
			count++
		}
		return tx.Model(&group).UpdateColumn("expected_count", gorm.Expr("expected_count + ?", len(members))).Error
	})
	return count, err
}

// ReplaceMembers is the safe roster import path. A roster can only be
// replaced before the group is confirmed; once tickets or admission facts
// exist, deleting rows would orphan entitlements and audit history.
func (s *TeamService) ReplaceMembers(tenantID, groupID uint, members []model.TourGroupMember) (int, error) {
	returnCount := len(members)
	err := model.Write(func(tx *gorm.DB) error {
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
		if err := tx.Where("id = ? AND supplier_tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
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
			if err := tx.Where("id = ? AND group_id = ? AND status = ?", memberID, groupID, "ticketed").First(&member).Error; err != nil {
				return fmt.Errorf("member %d is not ticketed", memberID)
			}
			var ticket model.Ticket
			if err := tx.Preload("OrderItem").Where("ticket_code = ? AND order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status IN ?", member.TicketCode, order.ID, group.SupplierTenantID, group.ScenicAreaID, []string{"unused", "active"}).First(&ticket).Error; err != nil {
				return fmt.Errorf("member %d has no valid ticket entitlement", memberID)
			}
			if ticket.OrderItem.UseDate == nil || !sameTeamDate(*ticket.OrderItem.UseDate, group.VisitDate) {
				return fmt.Errorf("member %d ticket visit date does not match team", memberID)
			}
			if ticket.CodeMode == "order" {
				return errors.New("team admission requires one ticket entitlement per member")
			}
			var rule model.TicketRule
			if ticket.RuleSnapshot == "" || json.Unmarshal([]byte(ticket.RuleSnapshot), &rule) != nil {
				return errors.New("ticket entitlement has no valid rule snapshot")
			}
			if groupMatch, itemMatch := matchRule(rule, checkpointID); groupMatch == nil || itemMatch == nil {
				return errors.New("entry checkpoint is not allowed by ticket entitlement")
			}
			if err := tx.Create(&model.CheckInRecord{TenantID: group.SupplierTenantID, ScenicAreaID: group.ScenicAreaID, TicketCode: ticket.TicketCode, TicketID: ticket.ID, CheckPointID: checkpointID, DeviceID: device.ID, CheckInTime: now, Result: "success", Message: "team admission"}).Error; err != nil {
				return err
			}
			if err := tx.Model(&ticket).Updates(map[string]interface{}{"status": "used", "check_in_count": gorm.Expr("check_in_count + 1")}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ? AND fulfillment_order_id = ?", ticket.ID, ticket.FulfillmentOrderID).Update("status", "used").Error; err != nil {
				return err
			}
			if err := tx.Model(&member).Updates(map[string]interface{}{"status": "entered", "entered_at": now, "entry_batch_no": batch.BatchNo}).Error; err != nil {
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
		var group model.TourGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
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
		var members []model.TourGroupMember
		if err := tx.Where("group_id = ? AND status = ?", group.ID, "planned").Order("id").Find(&members).Error; err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Where("order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status = ? AND code_mode != ?", order.ID, group.SupplierTenantID, group.ScenicAreaID, "unused", "order").Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		if len(tickets) < len(members) {
			return errors.New("order does not have enough member ticket entitlements")
		}
		for i := range members {
			if err := tx.Model(&members[i]).Updates(map[string]interface{}{"ticket_code": tickets[i].TicketCode, "status": "ticketed"}).Error; err != nil {
				return err
			}
		}
		amountCents := teamOrderSettlementCents(&order, group.SupplierTenantID, group.ScenicAreaID)
		if amountCents <= 0 {
			return errors.New("order has no settlement amount for the team supplier and scenic area")
		}
		creditUsed := amountCents - group.DepositCents
		if creditUsed < 0 {
			creditUsed = 0
		}
		if group.ContractID != 0 {
			var contract model.TravelContract
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&contract, group.ContractID).Error; err != nil {
				return err
			}
			var occupied int64
			if err := tx.Model(&model.TourGroup{}).
				Where("contract_id = ? AND id != ? AND status != ? AND settlement_status != ?", contract.ID, group.ID, "cancelled", "settled").
				Select("COALESCE(SUM(credit_used_cents), 0)").Scan(&occupied).Error; err != nil {
				return err
			}
			if contract.CreditLimitCents > 0 && occupied+creditUsed > contract.CreditLimitCents {
				return errors.New("team order exceeds contract credit limit")
			}
		}
		return tx.Model(&group).Updates(map[string]interface{}{"sales_order_id": order.ID, "status": "confirmed", "contract_amount_cents": amountCents, "credit_used_cents": creditUsed, "settlement_status": "open"}).Error
	})
}

func teamOrderSettlementCents(order *model.Order, supplierTenantID, scenicAreaID uint) int64 {
	if order == nil {
		return 0
	}
	var total int64
	for i := range order.Items {
		item := order.Items[i]
		if item.FulfillmentTenantID != supplierTenantID || item.FulfillmentScenicAreaID != scenicAreaID {
			continue
		}
		total += moneyCents(item.SettlementPrice) * int64(item.Quantity)
	}
	return total
}

func sameTeamDate(left, right time.Time) bool {
	return startOfDay(left).Equal(startOfDay(right))
}

func validateTeamContractTx(tx *gorm.DB, tenantID uint, group *model.TourGroup) error {
	if group == nil || group.SupplierTenantID == 0 || group.VisitDate.IsZero() {
		return errors.New("team supplier and visit date are required")
	}
	if tenantID == group.SupplierTenantID {
		return requireActiveTenantCapability(tx, tenantID, "supplier")
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
