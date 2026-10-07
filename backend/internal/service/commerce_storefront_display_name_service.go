package service

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCommerceStorefrontDisplayNameInvalid = errors.New("commerce storefront display name is invalid")

type CommerceStorefrontDisplayNameInput struct {
	DisplayName string `json:"display_name"`
	Reason      string `json:"reason"`
}

type CommerceStorefrontDisplayNameView struct {
	ChannelAccountID uint   `json:"channel_account_id"`
	DisplayName      string `json:"display_name"`
}

// SaveChannelDisplayName changes presentation only. The account's credentials,
// business bindings, fulfillment locations and existing orders remain independent.
func (s *CommerceStorefrontService) SaveChannelDisplayName(tenantID, channelAccountID uint, input CommerceStorefrontDisplayNameInput, actorID uint, actorRole string) (*CommerceStorefrontDisplayNameView, error) {
	if tenantID == 0 || channelAccountID == 0 {
		return nil, ErrTenantUnavailable
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 120 ||
		strings.ContainsFunc(input.DisplayName, unicode.IsControl) ||
		input.Reason == "" || len([]rune(input.Reason)) > 255 {
		return nil, ErrCommerceStorefrontDisplayNameInvalid
	}
	var result *CommerceStorefrontDisplayNameView
	err := s.db().Transaction(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND type = ?", channelAccountID, tenantID, "wechat_miniapp").
			First(&account).Error; err != nil {
			return err
		}
		before := CommerceStorefrontDisplayNameView{ChannelAccountID: account.ID, DisplayName: account.StorefrontDisplayName}
		if err := tx.Model(&account).Update("storefront_display_name", input.DisplayName).Error; err != nil {
			return err
		}
		after := CommerceStorefrontDisplayNameView{ChannelAccountID: account.ID, DisplayName: input.DisplayName}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if err := recordAuditTx(tx, actorID, tenantID, actorRole, "tenant", "commerce.storefront_display_name.save", "channel_account", account.ID, input.Reason, string(beforeJSON), string(afterJSON)); err != nil {
			return err
		}
		result = &after
		return nil
	})
	return result, err
}
