package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUpstreamSupplyNotReady = errors.New("上游供票连接未完成配置")

type UpstreamSupplyService struct{}

type UpstreamConnectionInput struct {
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Endpoint    string `json:"endpoint"`
	CorpCode    string `json:"corp_code"`
	Username    string `json:"username"`
	PrivateKey  string `json:"private_key"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
}
type ProductSupplyInput struct {
	Enabled              bool   `json:"enabled"`
	UpstreamConnectionID uint   `json:"upstream_connection_id"`
	ExternalProductCode  string `json:"external_product_code"`
}
type ProductSupplyView struct {
	ProductID            uint   `json:"product_id"`
	Enabled              bool   `json:"enabled"`
	UpstreamConnectionID uint   `json:"upstream_connection_id"`
	ExternalProductCode  string `json:"external_product_code"`
	Ready                bool   `json:"ready"`
	Status               string `json:"status"`
}

func completeConnection(c *model.UpstreamConnection) bool {
	return c != nil && c.Provider == "zhiyoubao" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Endpoint)), "https://") && strings.TrimSpace(c.CorpCode) != "" && strings.TrimSpace(c.Username) != "" && strings.TrimSpace(c.PrivateKeyCiphertext) != ""
}
func setConnectionResponse(c *model.UpstreamConnection) *model.UpstreamConnection {
	if c != nil {
		c.CredentialsConfigured = completeConnection(c)
		c.PrivateKeyCiphertext = ""
	}
	return c
}

func (UpstreamSupplyService) ListConnections(tenantID uint) ([]model.UpstreamConnection, error) {
	if err := requireActiveScenicSupplier(model.DB, tenantID); err != nil {
		return nil, err
	}
	var rows []model.UpstreamConnection
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		setConnectionResponse(&rows[i])
	}
	return rows, nil
}

func normalizeConnectionInput(input *UpstreamConnectionInput) error {
	input.Name, input.Provider, input.Endpoint, input.CorpCode, input.Username, input.Environment, input.Status = strings.TrimSpace(input.Name), strings.TrimSpace(input.Provider), strings.TrimSpace(input.Endpoint), strings.TrimSpace(input.CorpCode), strings.TrimSpace(input.Username), strings.TrimSpace(input.Environment), strings.TrimSpace(input.Status)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || input.Provider != "zhiyoubao" {
		return errors.New("请填写连接名称并选择智游宝供应系统")
	}
	if input.Endpoint != "" {
		u, err := url.Parse(input.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("供应连接地址无效")
		}
	}
	if input.Environment == "" {
		input.Environment = "production"
	}
	if input.Environment != "production" && input.Environment != "sandbox" {
		return errors.New("供应连接环境无效")
	}
	if input.Status != "" && input.Status != "draft" && input.Status != "active" && input.Status != "disabled" {
		return errors.New("供应连接状态无效")
	}
	return nil
}

func (UpstreamSupplyService) CreateConnection(tenantID, actorID uint, role string, input UpstreamConnectionInput) (*model.UpstreamConnection, error) {
	if err := normalizeConnectionInput(&input); err != nil {
		return nil, err
	}
	cipherText := ""
	var err error
	if input.PrivateKey != "" {
		cipherText, err = utils.EncryptAES(input.PrivateKey)
		if err != nil {
			return nil, err
		}
	}
	row := model.UpstreamConnection{TenantID: tenantID, Name: input.Name, Provider: input.Provider, Endpoint: input.Endpoint, CorpCode: input.CorpCode, Username: input.Username, PrivateKeyCiphertext: cipherText, Environment: input.Environment, Status: "draft"}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "upstream.connection.create", "upstream_connection", row.ID, "创建智游宝供应连接草稿", "", "")
	})
	return setConnectionResponse(&row), err
}

func (UpstreamSupplyService) UpdateConnection(tenantID, id, actorID uint, role string, input UpstreamConnectionInput) (*model.UpstreamConnection, error) {
	if err := normalizeConnectionInput(&input); err != nil {
		return nil, err
	}
	var row model.UpstreamConnection
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&row).Error; err != nil {
			return err
		}
		if input.Environment != row.Environment {
			return errors.New("供应连接环境不可修改")
		}
		updates := map[string]interface{}{"name": input.Name, "endpoint": input.Endpoint, "corp_code": input.CorpCode, "username": input.Username}
		if row.Endpoint != input.Endpoint || row.CorpCode != input.CorpCode || row.Username != input.Username {
			var sold int64
			if err := tx.Model(&model.OrderItemSupplySnapshot{}).Where("connection_id = ? AND mode = 'upstream'", row.ID).Count(&sold).Error; err != nil {
				return err
			}
			if sold > 0 {
				return errors.New("此连接已有订单，接口或账号变更请新建连接；原连接可轮换密钥")
			}
		}
		if input.PrivateKey != "" {
			cipherText, err := utils.EncryptAES(input.PrivateKey)
			if err != nil {
				return err
			}
			updates["private_key_ciphertext"] = cipherText
		}
		if input.Status != "" {
			if input.Status == "active" {
				candidate := row
				candidate.Endpoint, candidate.CorpCode, candidate.Username = input.Endpoint, input.CorpCode, input.Username
				if v, ok := updates["private_key_ciphertext"].(string); ok {
					candidate.PrivateKeyCiphertext = v
				}
				if !completeConnection(&candidate) {
					return ErrUpstreamSupplyNotReady
				}
			}
			updates["status"] = input.Status
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "upstream.connection.update", "upstream_connection", id, "更新智游宝供应连接", "", "")
	})
	if err != nil {
		return nil, err
	}
	if err := model.DB.Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return setConnectionResponse(&row), nil
}

func (UpstreamSupplyService) SetConnectionStatus(tenantID, id, actorID uint, role, status string) error {
	if status != "active" && status != "disabled" && status != "draft" {
		return errors.New("供应连接状态无效")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.UpstreamConnection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&row).Error; err != nil {
			return err
		}
		if status == "active" && !completeConnection(&row) {
			return ErrUpstreamSupplyNotReady
		}
		if err := tx.Model(&row).Update("status", status).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "upstream.connection.status", "upstream_connection", id, "变更智游宝供应连接状态", "", fmt.Sprintf(`{"status":%q}`, status))
	})
}

func supplyProductTx(tx *gorm.DB, tenantID, productID uint, lock bool) (*model.Product, error) {
	if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
		return nil, err
	}
	var product model.Product
	query := tx.Where("id = ? AND tenant_id = ?", productID, tenantID)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.First(&product).Error; err != nil {
		return nil, err
	}
	if isDistributedListing(&product) || product.ProductKind != "ticket" || product.ScenicAreaID == 0 {
		return nil, errors.New("只能为当前景区自有普通门票配置上游供应")
	}
	return &product, nil
}

func productSupplyViewTx(tx *gorm.DB, tenantID, productID uint) (*ProductSupplyView, error) {
	view := &ProductSupplyView{ProductID: productID, Status: "local"}
	var config model.ProductSupplyConfig
	err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).First(&config).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	view.Enabled = config.Enabled || config.ActiveMappingID != nil
	if config.ActiveMappingID != nil {
		var mapping model.UpstreamProductMapping
		if err := tx.Where("id = ? AND tenant_id = ? AND product_id = ?", *config.ActiveMappingID, tenantID, productID).First(&mapping).Error; err == nil {
			view.UpstreamConnectionID, view.ExternalProductCode, view.Status = mapping.UpstreamConnectionID, mapping.ExternalProductCode, mapping.Status
		}
	}
	if !view.Enabled {
		var mapping model.UpstreamProductMapping
		if err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Order("CASE WHEN status = 'draft' THEN 0 ELSE 1 END, id DESC").First(&mapping).Error; err == nil {
			view.UpstreamConnectionID, view.ExternalProductCode, view.Status = mapping.UpstreamConnectionID, mapping.ExternalProductCode, "draft"
		}
	}
	if view.Enabled {
		var conn model.UpstreamConnection
		if err := tx.Where("id = ? AND tenant_id = ?", view.UpstreamConnectionID, tenantID).First(&conn).Error; err == nil {
			view.Ready = conn.Status == "active" && completeConnection(&conn)
		}
	}
	return view, nil
}

func (UpstreamSupplyService) GetProduct(tenantID, productID uint) (*ProductSupplyView, error) {
	if _, err := supplyProductTx(model.DB, tenantID, productID, false); err != nil {
		return nil, err
	}
	return productSupplyViewTx(model.DB, tenantID, productID)
}

func (UpstreamSupplyService) SetProduct(tenantID, productID, actorID uint, role string, input ProductSupplyInput) (*ProductSupplyView, error) {
	input.ExternalProductCode = strings.TrimSpace(input.ExternalProductCode)
	if utf8.RuneCountInString(input.ExternalProductCode) > 200 {
		return nil, errors.New("上游商品编码过长")
	}
	var result *ProductSupplyView
	err := model.Write(func(tx *gorm.DB) error {
		product, err := supplyProductTx(tx, tenantID, productID, true)
		if err != nil {
			return err
		}
		if input.Enabled && product.Type == "offline" {
			return errors.New("窗口票只使用本系统出票，不支持启用上游供应")
		}
		beforeView, err := productSupplyViewTx(tx, tenantID, productID)
		if err != nil {
			return err
		}
		var config model.ProductSupplyConfig
		err = tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).First(&config).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		config.TenantID, config.ProductID = tenantID, productID
		if !input.Enabled {
			config.Enabled, config.ActiveMappingID = false, nil
			if input.UpstreamConnectionID != 0 || input.ExternalProductCode != "" {
				if input.UpstreamConnectionID == 0 || input.ExternalProductCode == "" {
					return errors.New("请同时填写供应连接和上游商品编码")
				}
				var conn model.UpstreamConnection
				if err := tx.Where("id = ? AND tenant_id = ? AND provider = ?", input.UpstreamConnectionID, tenantID, "zhiyoubao").First(&conn).Error; err != nil {
					return errors.New("供应连接不存在")
				}
				var mapping model.UpstreamProductMapping
				if err := tx.Where("tenant_id = ? AND product_id = ? AND status = 'draft'", tenantID, productID).First(&mapping).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				mapping.TenantID, mapping.ProductID, mapping.UpstreamConnectionID, mapping.ExternalProductCode, mapping.Status = tenantID, productID, input.UpstreamConnectionID, input.ExternalProductCode, "draft"
				if err := tx.Save(&mapping).Error; err != nil {
					return err
				}
			}
			if err := tx.Save(&config).Error; err != nil {
				return err
			}
		} else {
			if input.UpstreamConnectionID == 0 || input.ExternalProductCode == "" {
				return errors.New("启用上游供应必须填写连接和商品编码")
			}
			var conn model.UpstreamConnection
			if err := tx.Where("id = ? AND tenant_id = ? AND provider = ?", input.UpstreamConnectionID, tenantID, "zhiyoubao").First(&conn).Error; err != nil {
				return errors.New("供应连接不存在")
			}
			if conn.Status != "active" || !completeConnection(&conn) {
				return ErrUpstreamSupplyNotReady
			}
			mapping := model.UpstreamProductMapping{TenantID: tenantID, ProductID: productID, UpstreamConnectionID: input.UpstreamConnectionID, ExternalProductCode: input.ExternalProductCode, Status: "active"}
			if err := tx.Create(&mapping).Error; err != nil {
				return err
			}
			config.Enabled, config.ActiveMappingID = true, &mapping.ID
			if err := tx.Save(&config).Error; err != nil {
				return err
			}
		}
		result, err = productSupplyViewTx(tx, tenantID, productID)
		if err != nil {
			return err
		}
		before, _ := json.Marshal(beforeView)
		after, _ := json.Marshal(result)
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "product.supply.configure", "product", productID, "配置智游宝供应（仅影响新订单）", string(before), string(after))
	})
	return result, err
}

var zybPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

func resolveProductSupplyTx(tx *gorm.DB, product *model.Product, environment, contactName, contactPhone string, quantity int) (bool, error) {
	if product == nil {
		return false, errors.New("product is required")
	}
	var config model.ProductSupplyConfig
	if err := tx.Where("tenant_id = ? AND product_id = ? AND enabled = true AND active_mapping_id IS NOT NULL", product.TenantID, product.ID).First(&config).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var mapping model.UpstreamProductMapping
	if err := tx.Where("id = ? AND tenant_id = ? AND product_id = ? AND status = 'active'", *config.ActiveMappingID, product.TenantID, product.ID).First(&mapping).Error; err != nil {
		return false, ErrUpstreamSupplyNotReady
	}
	var conn model.UpstreamConnection
	if err := tx.Where("id = ? AND tenant_id = ? AND status = 'active'", mapping.UpstreamConnectionID, product.TenantID).First(&conn).Error; err != nil || !completeConnection(&conn) || conn.Environment != environment {
		return false, ErrUpstreamSupplyNotReady
	}
	if quantity <= 0 {
		return false, errors.New("智游宝供票数量必须大于零")
	}
	if strings.TrimSpace(contactName) == "" || utf8.RuneCountInString(strings.TrimSpace(contactName)) > 50 || !zybPhonePattern.MatchString(strings.TrimSpace(contactPhone)) {
		return false, errors.New("智游宝订单联系人姓名和手机号必填且格式有效")
	}
	return true, nil
}
