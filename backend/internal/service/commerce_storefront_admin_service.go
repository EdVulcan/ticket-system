package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CommerceStorefrontBindingInput is the small tenant-admin configuration
// surface for publishing a commercial business through a WeChat channel.
// Credentials remain owned by ChannelService; this input only selects an
// existing account, business capability and fulfillment location.
type CommerceStorefrontBindingInput struct {
	ID               uint   `json:"id,omitempty"`
	ChannelAccountID uint   `json:"channel_account_id"`
	BusinessType     string `json:"business_type"`
	LocationID       uint   `json:"location_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
}

type CommerceStorefrontBindingView struct {
	ID               uint   `json:"id"`
	TenantID         uint   `json:"tenant_id"`
	ChannelAccountID uint   `json:"channel_account_id"`
	ChannelCode      string `json:"channel_code"`
	AppID            string `json:"app_id"`
	AccountStatus    string `json:"account_status"`
	Environment      string `json:"environment"`
	CredentialsReady bool   `json:"credentials_ready"`
	BusinessType     string `json:"business_type"`
	LocationID       uint   `json:"location_id"`
	LocationName     string `json:"location_name"`
	Status           string `json:"status"`
}

// CommerceStorefrontChannelView is the safe account selector exposed to a
// commercial tenant. It intentionally contains no secret or protocol
// ciphertext; credentials remain managed by the channel-account boundary.
type CommerceStorefrontChannelView struct {
	ID               uint   `json:"id"`
	Code             string `json:"code"`
	AppID            string `json:"app_id"`
	Status           string `json:"status"`
	Environment      string `json:"environment"`
	CredentialsReady bool   `json:"credentials_ready"`
}

var (
	ErrCommerceStorefrontBindingInvalid = errors.New("commerce storefront binding is invalid")
	ErrCommerceStorefrontBindingAccount = errors.New("commerce storefront channel account is invalid")
)

func (s *CommerceStorefrontService) ListChannelAccounts(tenantID uint) ([]CommerceStorefrontChannelView, error) {
	if tenantID == 0 {
		return nil, ErrTenantUnavailable
	}
	var accounts []model.ChannelAccount
	if err := s.db().Where("tenant_id = ? AND type = ?", tenantID, "wechat_miniapp").Order("created_at DESC").Find(&accounts).Error; err != nil {
		return nil, err
	}
	result := make([]CommerceStorefrontChannelView, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, CommerceStorefrontChannelView{
			ID: account.ID, Code: account.Code, AppID: account.AppID, Status: account.Status,
			Environment:      account.Environment,
			CredentialsReady: strings.TrimSpace(account.AppID) != "" && strings.TrimSpace(account.SecretCiphertext) != "",
		})
	}
	return result, nil
}

func (s *CommerceStorefrontService) ListBindings(tenantID uint, businessType string) ([]CommerceStorefrontBindingView, error) {
	if tenantID == 0 {
		return nil, ErrTenantUnavailable
	}
	businessType = strings.TrimSpace(businessType)
	if businessType != "" && !validTenantBusinessType(businessType) {
		return nil, ErrCommerceStorefrontBindingInvalid
	}
	query := s.db().Where("tenant_id = ?", tenantID)
	if businessType != "" {
		query = query.Where("business_type = ?", businessType)
	}
	var bindings []model.CommerceStorefrontBinding
	if err := query.Order("created_at DESC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	result := make([]CommerceStorefrontBindingView, 0, len(bindings))
	for i := range bindings {
		view, err := s.storefrontBindingView(s.db(), &bindings[i])
		if err != nil {
			return nil, err
		}
		result = append(result, *view)
	}
	return result, nil
}

func (s *CommerceStorefrontService) storefrontBindingView(tx *gorm.DB, binding *model.CommerceStorefrontBinding) (*CommerceStorefrontBindingView, error) {
	if binding == nil || binding.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var account model.ChannelAccount
	if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", binding.ChannelAccountID, binding.TenantID, "wechat_miniapp").First(&account).Error; err != nil {
		return nil, err
	}
	var location model.CommerceFulfillmentLocation
	if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ?", binding.LocationID, binding.TenantID, binding.BusinessType).First(&location).Error; err != nil {
		return nil, err
	}
	return &CommerceStorefrontBindingView{
		ID: binding.ID, TenantID: binding.TenantID, ChannelAccountID: account.ID,
		ChannelCode: account.Code, AppID: account.AppID, AccountStatus: account.Status,
		Environment:      account.Environment,
		CredentialsReady: strings.TrimSpace(account.AppID) != "" && strings.TrimSpace(account.SecretCiphertext) != "",
		BusinessType:     binding.BusinessType, LocationID: location.ID, LocationName: location.Name,
		Status: binding.Status,
	}, nil
}

func normalizeCommerceStorefrontBindingInput(input CommerceStorefrontBindingInput) (CommerceStorefrontBindingInput, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.ChannelAccountID == 0 || input.LocationID == 0 || !validTenantBusinessType(input.BusinessType) || (input.Status != "active" && input.Status != "disabled") || input.Reason == "" || len([]rune(input.Reason)) > 255 {
		return input, ErrCommerceStorefrontBindingInvalid
	}
	return input, nil
}

// SaveBinding creates or updates one published binding. The caller supplies
// only the authenticated tenant and operator context; account ownership,
// credentials, capability and location compatibility are all rechecked here.
func (s *CommerceStorefrontService) SaveBinding(tenantID uint, input CommerceStorefrontBindingInput, actorID uint, actorRole string) (*CommerceStorefrontBindingView, error) {
	if tenantID == 0 {
		return nil, ErrTenantUnavailable
	}
	var err error
	input, err = normalizeCommerceStorefrontBindingInput(input)
	if err != nil {
		return nil, err
	}
	var result *CommerceStorefrontBindingView
	err = s.db().Transaction(func(tx *gorm.DB) error {
		if input.Status == "active" {
			if err := RequireActiveTenantBusinessCapability(tx, tenantID, input.BusinessType); err != nil {
				return err
			}
		} else if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, input.BusinessType); err != nil {
			return err
		}
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", input.ChannelAccountID, tenantID, "wechat_miniapp").First(&account).Error; err != nil {
			return ErrCommerceStorefrontBindingAccount
		}
		if account.Status == "disabled" || strings.TrimSpace(account.AppID) == "" || (input.Status == "active" && strings.TrimSpace(account.SecretCiphertext) == "") {
			return ErrCommerceStorefrontBindingAccount
		}
		var location model.CommerceFulfillmentLocation
		if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ?", input.LocationID, tenantID, input.BusinessType).First(&location).Error; err != nil {
			return ErrCommerceStorefrontBindingInvalid
		}
		if input.Status == "active" && location.Status != "active" {
			return ErrCommerceStorefrontBindingInvalid
		}

		var before *CommerceStorefrontBindingView
		var binding model.CommerceStorefrontBinding
		if input.ID != 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", input.ID, tenantID).First(&binding).Error; err != nil {
				return err
			}
			before, err = s.storefrontBindingView(tx, &binding)
			if err != nil {
				return err
			}
			if err := tx.Model(&binding).Updates(map[string]interface{}{
				"channel_account_id": input.ChannelAccountID, "business_type": input.BusinessType,
				"location_id": input.LocationID, "status": input.Status,
			}).Error; err != nil {
				return err
			}
			binding.ChannelAccountID, binding.BusinessType, binding.LocationID, binding.Status = input.ChannelAccountID, input.BusinessType, input.LocationID, input.Status
		} else {
			binding = model.CommerceStorefrontBinding{
				TenantID: tenantID, ChannelAccountID: input.ChannelAccountID, BusinessType: input.BusinessType,
				LocationID: input.LocationID, Status: input.Status,
			}
			if err := tx.Create(&binding).Error; err != nil {
				return err
			}
		}
		after, viewErr := s.storefrontBindingView(tx, &binding)
		if viewErr != nil {
			return viewErr
		}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if err := recordAuditTx(tx, actorID, tenantID, actorRole, "tenant", "commerce.storefront_binding.save", "commerce_storefront_binding", binding.ID, input.Reason, string(beforeJSON), string(afterJSON)); err != nil {
			return err
		}
		result = after
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx_commerce_storefront_bindings_account") || strings.Contains(err.Error(), "duplicate key") {
			return nil, fmt.Errorf("%w: channel account already has a storefront binding", ErrCommerceStorefrontBindingInvalid)
		}
		return nil, err
	}
	return result, nil
}
