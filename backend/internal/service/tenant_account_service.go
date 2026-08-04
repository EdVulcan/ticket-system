package service

import (
	"errors"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

var (
	ErrInitialTenantAdmin = errors.New("初始商户管理员只能由本人修改密码")
	ErrOwnTenantAccount   = errors.New("请通过个人账户菜单修改自己的密码")
)

func UpdateTenantUserRole(tenantID, targetUserID, actorUserID uint, role string) error {
	if !authz.IsDelegatedTenantRole(role) {
		return errors.New("invalid user role")
	}
	if targetUserID == actorUserID {
		return ErrOwnTenantAccount
	}
	return model.Write(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Where("id = ? AND tenant_id = ?", targetUserID, tenantID).First(&user).Error; err != nil {
			return err
		}
		if user.IsInitialAdmin {
			return ErrInitialTenantAdmin
		}
		return tx.Model(&user).Updates(map[string]interface{}{
			"role": role, "token_version": gorm.Expr("token_version + 1"),
		}).Error
	})
}

func ResetTenantUserPassword(tenantID, targetUserID, actorUserID uint, password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	if targetUserID == actorUserID {
		return ErrOwnTenantAccount
	}
	hashedPassword, err := HashPassword(password)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Where("id = ? AND tenant_id = ?", targetUserID, tenantID).First(&user).Error; err != nil {
			return err
		}
		if user.IsInitialAdmin {
			return ErrInitialTenantAdmin
		}
		return tx.Model(&user).Updates(map[string]interface{}{
			"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
		}).Error
	})
}
