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

var ErrCommerceStorefrontContactInvalid = errors.New("commerce storefront contact is invalid")

type CommerceStorefrontContactInput struct {
	ContactType string `json:"contact_type"`
	ContactName string `json:"contact_name"`
	WechatID    string `json:"wechat_id"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	ClearQRCode bool   `json:"clear_qr_code"`
}

type CommerceStorefrontContactView struct {
	ChannelAccountID uint   `json:"channel_account_id"`
	ContactType      string `json:"contact_type"`
	ContactName      string `json:"contact_name"`
	WechatID         string `json:"wechat_id,omitempty"`
	QRCodeURL        string `json:"qr_code_url,omitempty"`
	Status           string `json:"status"`
	Available        bool   `json:"available"`
}

func normalizeCommerceStorefrontContactInput(input CommerceStorefrontContactInput) (CommerceStorefrontContactInput, error) {
	input.ContactType = strings.TrimSpace(input.ContactType)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.WechatID = strings.TrimSpace(input.WechatID)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ContactType == "" {
		input.ContactType = "personal_wechat"
	}
	if input.Status == "" {
		input.Status = "disabled"
	}
	if (input.ContactType != "personal_wechat" && input.ContactType != "enterprise_wechat") ||
		(input.Status != "active" && input.Status != "disabled") || input.Reason == "" ||
		len([]rune(input.ContactName)) > 80 || len([]rune(input.WechatID)) > 80 || len([]rune(input.Reason)) > 255 {
		return input, ErrCommerceStorefrontContactInvalid
	}
	return input, nil
}

func commerceStorefrontContactView(account *model.ChannelAccount, public bool) *CommerceStorefrontContactView {
	if account == nil {
		return nil
	}
	available := account.StorefrontContactStatus == "active" && strings.TrimSpace(account.StorefrontContactQRCodeURL) != ""
	view := &CommerceStorefrontContactView{
		ChannelAccountID: account.ID,
		ContactType:      account.StorefrontContactType,
		ContactName:      account.StorefrontContactName,
		WechatID:         account.StorefrontWechatID,
		QRCodeURL:        account.StorefrontContactQRCodeURL,
		Status:           account.StorefrontContactStatus,
		Available:        available,
	}
	if public && !available {
		view.ContactType = ""
		view.ContactName = ""
		view.WechatID = ""
		view.QRCodeURL = ""
	}
	return view
}

func commerceStorefrontContactAccountEnabled(status string) bool {
	return status == "active" || status == "sandbox"
}

// contactView re-checks the managed image at read time. The database value is
// not enough to prove that a public QR code still exists: an operator may have
// removed the upload, or an old row may contain an unmanaged URL. Returning a
// redacted projection is safer than handing a dead or external URL to a
// customer.
func (s *CommerceStorefrontService) contactView(account *model.ChannelAccount, public bool) *CommerceStorefrontContactView {
	view := commerceStorefrontContactView(account, false)
	if view == nil {
		return nil
	}
	view.Available = view.Status == "active" && commerceStorefrontContactAccountEnabled(account.Status) &&
		strings.TrimSpace(view.QRCodeURL) != "" && s != nil && s.ContactImages != nil &&
		s.ContactImages.ValidateOwnedURL(account.TenantID, account.ID, view.QRCodeURL) == nil
	if public && !view.Available {
		view.ContactType = ""
		view.ContactName = ""
		view.WechatID = ""
		view.QRCodeURL = ""
	}
	return view
}

func (s *CommerceStorefrontService) GetChannelContact(tenantID, channelAccountID uint) (*CommerceStorefrontContactView, error) {
	if tenantID == 0 || channelAccountID == 0 {
		return nil, ErrTenantUnavailable
	}
	var account model.ChannelAccount
	if err := s.db().Where("id = ? AND tenant_id = ? AND type = ?", channelAccountID, tenantID, "wechat_miniapp").First(&account).Error; err != nil {
		return nil, err
	}
	return s.contactView(&account, false), nil
}

// SaveChannelContact updates the contact presentation owned by one WeChat
// channel account. Restaurant and retail bindings for that account therefore
// resolve the same contact. A replacement image is written before the database
// transaction and removed again if the authoritative update cannot commit.
func (s *CommerceStorefrontService) SaveChannelContact(tenantID, channelAccountID uint, input CommerceStorefrontContactInput, imageData []byte, actorID uint, actorRole string) (*CommerceStorefrontContactView, error) {
	if tenantID == 0 || channelAccountID == 0 {
		return nil, ErrTenantUnavailable
	}
	var err error
	input, err = normalizeCommerceStorefrontContactInput(input)
	if err != nil {
		return nil, err
	}
	if len(imageData) > 0 && s.ContactImages == nil {
		return nil, fmt.Errorf("%w: contact image store is unavailable", ErrCommerceStorefrontContactInvalid)
	}

	var (
		newImageURL string
		oldImageURL string
		result      *CommerceStorefrontContactView
	)
	if len(imageData) > 0 {
		newImageURL, err = s.ContactImages.Save(tenantID, channelAccountID, imageData)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCommerceStorefrontContactInvalid, err)
		}
	}
	cleanupNewImage := func() {
		if newImageURL != "" && s.ContactImages != nil {
			_ = s.ContactImages.RemoveOwnedURL(tenantID, channelAccountID, newImageURL)
		}
	}

	err = s.db().Transaction(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", channelAccountID, tenantID, "wechat_miniapp").First(&account).Error; err != nil {
			return err
		}
		before := commerceStorefrontContactView(&account, false)
		oldImageURL = account.StorefrontContactQRCodeURL
		finalImageURL := oldImageURL
		if input.ClearQRCode {
			finalImageURL = ""
		}
		if newImageURL != "" {
			finalImageURL = newImageURL
		}
		if input.Status == "active" && finalImageURL == "" {
			return fmt.Errorf("%w: active contact requires a QR code", ErrCommerceStorefrontContactInvalid)
		}
		if finalImageURL != "" {
			if s.ContactImages == nil {
				return fmt.Errorf("%w: contact image store is unavailable", ErrCommerceStorefrontContactInvalid)
			}
			if err := s.ContactImages.ValidateOwnedURL(tenantID, channelAccountID, finalImageURL); err != nil {
				return fmt.Errorf("%w: %v", ErrCommerceStorefrontContactInvalid, err)
			}
		}
		if err := tx.Model(&account).Updates(map[string]interface{}{
			"storefront_contact_type":        input.ContactType,
			"storefront_contact_name":        input.ContactName,
			"storefront_wechat_id":           input.WechatID,
			"storefront_contact_qr_code_url": finalImageURL,
			"storefront_contact_status":      input.Status,
		}).Error; err != nil {
			return err
		}
		account.StorefrontContactType = input.ContactType
		account.StorefrontContactName = input.ContactName
		account.StorefrontWechatID = input.WechatID
		account.StorefrontContactQRCodeURL = finalImageURL
		account.StorefrontContactStatus = input.Status
		after := commerceStorefrontContactView(&account, false)
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if err := recordAuditTx(tx, actorID, tenantID, actorRole, "tenant", "commerce.storefront_contact.save", "channel_account", account.ID, input.Reason, string(beforeJSON), string(afterJSON)); err != nil {
			return err
		}
		result = after
		return nil
	})
	if err != nil {
		cleanupNewImage()
		return nil, err
	}
	if oldImageURL != "" && oldImageURL != result.QRCodeURL && s.ContactImages != nil {
		_ = s.ContactImages.RemoveOwnedURL(tenantID, channelAccountID, oldImageURL)
	}
	return result, nil
}

// GetContact is the customer-safe projection. It authenticates the session,
// then requires at least one active business binding before exposing a channel
// account's active contact configuration.
func (s *CommerceStorefrontService) GetContact(token string) (*CommerceStorefrontContactView, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadBusinesses(s.db(), &context.Account, "", false); err != nil {
		return nil, err
	}
	return s.contactView(&context.Account, true), nil
}
