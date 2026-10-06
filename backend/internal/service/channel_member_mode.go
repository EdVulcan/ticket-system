package service

import (
	"errors"
	"fmt"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

var ErrChannelMemberModeInvalid = errors.New("invalid channel member mode")

// ChannelAllowsMemberIdentity is the single read-side policy check used by
// first-party login adapters. A missing value is intentionally denied so a
// partially migrated or hand-created account cannot silently enter the member
// graph.
func ChannelAllowsMemberIdentity(account *model.ChannelAccount) bool {
	return account != nil && account.MemberMode == model.ChannelMemberModeFirstParty
}

func validChannelMemberMode(mode string) bool {
	return mode == model.ChannelMemberModeDisabled || mode == model.ChannelMemberModeFirstParty
}

// SetMemberMode is only exposed through the platform-scoped controller. Tenant
// administrators can see the resulting mode on their channel list but cannot
// promote an external channel into a first-party customer source.
func (s *ChannelService) SetMemberMode(tenantID, accountID uint, mode string) error {
	if tenantID == 0 || accountID == 0 || !validChannelMemberMode(mode) {
		return ErrChannelMemberModeInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		return setMemberModeTx(tx, tenantID, accountID, mode)
	})
}

// SetMemberModeAudited applies the same tenant-scoped policy as SetMemberMode
// and records the operator-visible transition in the tenant audit stream.
// The caller must derive tenantID and actorID from the authenticated context.
func (s *ChannelService) SetMemberModeAudited(tenantID, accountID uint, mode string, actorID uint, actorRole string) error {
	if tenantID == 0 || accountID == 0 || actorID == 0 || !validChannelMemberMode(mode) {
		return ErrChannelMemberModeInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Select("id", "type", "member_mode").Where("id = ? AND tenant_id = ?", accountID, tenantID).First(&account).Error; err != nil {
			return err
		}
		previous := account.MemberMode
		if err := setMemberModeTx(tx, tenantID, accountID, mode); err != nil {
			return err
		}
		if previous == mode {
			return nil
		}
		return recordAuditTx(tx, actorID, tenantID, actorRole, "tenant", "channel.member_mode.update", "channel_account", accountID,
			"tenant-controlled first-party member source setting", fmt.Sprintf(`{"member_mode":%q}`, previous), fmt.Sprintf(`{"member_mode":%q}`, mode))
	})
}

func setMemberModeTx(tx *gorm.DB, tenantID, accountID uint, mode string) error {
	var account model.ChannelAccount
	if err := tx.Select("id", "type", "member_mode").Where("id = ? AND tenant_id = ?", accountID, tenantID).First(&account).Error; err != nil {
		return err
	}
	if mode == model.ChannelMemberModeFirstParty {
		switch account.Type {
		case "wechat_miniapp", "xiaohongshu", "app", "web":
		default:
			return errors.New("channel type is not an approved first-party member source")
		}
	}
	result := tx.Model(&model.ChannelAccount{}).
		Where("id = ? AND tenant_id = ?", accountID, tenantID).
		Update("member_mode", mode)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
