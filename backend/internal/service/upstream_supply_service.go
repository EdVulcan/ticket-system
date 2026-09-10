package service

import (
	"encoding/json"
	"errors"
	"strings"
	"ticket-backend/internal/model"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUpstreamSupplyNotReady = errors.New("上游供票尚未完成联调，该商品暂不能下单；不会改用本地出票")

type UpstreamSupplyService struct{}

type UpstreamConnectionInput struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
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

func (UpstreamSupplyService) ListConnections(tenantID uint) ([]model.UpstreamConnection, error) {
	if err := requireActiveScenicSupplier(model.DB, tenantID); err != nil {
		return nil, err
	}
	var rows []model.UpstreamConnection
	err := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (UpstreamSupplyService) CreateConnection(tenantID, actorID uint, role string, input UpstreamConnectionInput) (*model.UpstreamConnection, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || input.Provider != "zhiyoubao" {
		return nil, errors.New("请填写连接名称并选择智游宝供应系统")
	}
	row := model.UpstreamConnection{TenantID: tenantID, Name: input.Name, Provider: input.Provider, Status: "draft"}
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "upstream.connection.create", "upstream_connection", row.ID, "创建待联调的上游供应连接", "", "")
	})
	return &row, err
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
	if isDistributedListing(&product) || product.ProductKind != "ticket" {
		return nil, errors.New("只能为当前景区自有普通门票配置上游供应")
	}
	return &product, nil
}

func (UpstreamSupplyService) GetProduct(tenantID, productID uint) (*ProductSupplyView, error) {
	if _, err := supplyProductTx(model.DB, tenantID, productID, false); err != nil {
		return nil, err
	}
	return productSupplyViewTx(model.DB, tenantID, productID)
}

func productSupplyViewTx(tx *gorm.DB, tenantID, productID uint) (*ProductSupplyView, error) {
	view := &ProductSupplyView{ProductID: productID, Status: "local"}
	var config model.ProductSupplyConfig
	err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).First(&config).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	view.Enabled = config.ActiveMappingID != nil
	var mapping model.UpstreamProductMapping
	err = tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Order("id DESC").First(&mapping).Error
	if err == nil {
		view.UpstreamConnectionID, view.ExternalProductCode, view.Status = mapping.UpstreamConnectionID, mapping.ExternalProductCode, "draft"
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return view, nil
}

func (UpstreamSupplyService) SetProduct(tenantID, productID, actorID uint, role string, input ProductSupplyInput) (*ProductSupplyView, error) {
	input.ExternalProductCode = strings.TrimSpace(input.ExternalProductCode)
	if utf8.RuneCountInString(input.ExternalProductCode) > 200 {
		return nil, errors.New("上游商品编码过长")
	}
	var result *ProductSupplyView
	err := model.Write(func(tx *gorm.DB) error {
		if _, err := supplyProductTx(tx, tenantID, productID, true); err != nil {
			return err
		}
		if input.Enabled {
			return ErrUpstreamSupplyNotReady
		}
		if (input.UpstreamConnectionID == 0) != (input.ExternalProductCode == "") {
			return errors.New("请同时填写供应连接和上游商品编码")
		}
		if input.UpstreamConnectionID == 0 {
			return errors.New("请填写供应连接和上游商品编码；当前接口不支持清除供应草稿")
		}
		if input.UpstreamConnectionID != 0 {
			var connection model.UpstreamConnection
			if err := tx.Where("id = ? AND tenant_id = ? AND provider = ? AND status = ?", input.UpstreamConnectionID, tenantID, "zhiyoubao", "draft").First(&connection).Error; err != nil {
				return errors.New("供应连接不存在或不可用")
			}
		}
		var row model.ProductSupplyConfig
		err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		beforeView, err := productSupplyViewTx(tx, tenantID, productID)
		if err != nil {
			return err
		}
		before, _ := json.Marshal(beforeView)
		row.TenantID, row.ProductID = tenantID, productID
		row.ActiveMappingID = nil
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if input.UpstreamConnectionID != 0 {
			var mapping model.UpstreamProductMapping
			err := tx.Where("tenant_id = ? AND product_id = ? AND status = 'draft'", tenantID, productID).Order("id DESC").First(&mapping).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			mapping.TenantID, mapping.ProductID, mapping.UpstreamConnectionID = tenantID, productID, input.UpstreamConnectionID
			mapping.ExternalProductCode, mapping.Status = input.ExternalProductCode, "draft"
			if err := tx.Save(&mapping).Error; err != nil {
				return err
			}
		}
		result, err = productSupplyViewTx(tx, tenantID, productID)
		if err != nil {
			return err
		}
		after, _ := json.Marshal(result)
		return recordAuditTx(tx, actorID, tenantID, role, "tenant", "product.supply.configure", "product", productID, "配置门票上游供应（仅影响新订单）", string(before), string(after))
	})
	return result, err
}

func ensureLocalSupplyAvailableTx(tx *gorm.DB, product *model.Product) error {
	var count int64
	if err := tx.Model(&model.ProductSupplyConfig{}).Where("tenant_id = ? AND product_id = ? AND active_mapping_id IS NOT NULL", product.TenantID, product.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrUpstreamSupplyNotReady
	}
	return nil
}
