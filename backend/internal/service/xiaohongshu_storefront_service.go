package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrXiaohongshuStorefrontUnavailable = errors.New("xiaohongshu storefront is unavailable")

// XiaohongshuStorefrontService owns the account-level presentation image. It
// deliberately has no product relationship: product imagery remains owned by
// XiaohongshuProductConfig and cannot become a storefront fallback.
type XiaohongshuStorefrontService struct {
	Images XiaohongshuImageStore
}

func (s XiaohongshuStorefrontService) Get(tenantID, accountID uint) (string, error) {
	var account model.ChannelAccount
	if err := loadXiaohongshuStorefrontAccount(model.DB, tenantID, accountID, &account, false); err != nil {
		return "", err
	}
	return account.StorefrontImageURL, nil
}

func (s XiaohongshuStorefrontService) Upload(tenantID, accountID uint, data []byte) (string, error) {
	if err := loadXiaohongshuStorefrontAccount(model.DB, tenantID, accountID, nil, false); err != nil {
		return "", err
	}
	return s.Images.Save(tenantID, accountID, data)
}

func (s XiaohongshuStorefrontService) Set(tenantID, accountID, actorUserID uint, actorRole, imageURL string) (string, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL != "" {
		if err := s.Images.ValidateOwnedURL(tenantID, accountID, imageURL); err != nil {
			return "", err
		}
	}
	var stored model.ChannelAccount
	err := model.Write(func(tx *gorm.DB) error {
		if err := loadXiaohongshuStorefrontAccount(tx, tenantID, accountID, &stored, true); err != nil {
			return err
		}
		before := fmt.Sprintf(`{"image_url":%q}`, stored.StorefrontImageURL)
		after := fmt.Sprintf(`{"image_url":%q}`, imageURL)
		if err := tx.Model(&stored).Update("storefront_image_url", imageURL).Error; err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "xiaohongshu.storefront.image", "channel_account", stored.ID,
			"更新小红书店铺首图", before, after); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return imageURL, nil
}

func loadXiaohongshuStorefrontAccount(tx *gorm.DB, tenantID, accountID uint, destination *model.ChannelAccount, lock bool) error {
	if tenantID == 0 || accountID == 0 {
		return ErrXiaohongshuStorefrontUnavailable
	}
	if err := requireAnyActiveTenantCapability(tx, tenantID, "supplier", "distributor"); err != nil {
		return ErrXiaohongshuStorefrontUnavailable
	}
	query := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"})
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var account model.ChannelAccount
	if err := query.First(&account).Error; err != nil {
		return ErrXiaohongshuStorefrontUnavailable
	}
	if destination != nil {
		*destination = account
	}
	return nil
}
