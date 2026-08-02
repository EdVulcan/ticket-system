package service

import (
	"errors"
	"strings"
	"ticket-backend/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrCurrentPasswordInvalid = errors.New("当前密码不正确")
	ErrPasswordTooShort       = errors.New("新密码长度至少为8位")
	ErrPasswordUnchanged      = errors.New("新密码不能与当前密码相同")
)

type AccountService struct{}

func (s *AccountService) ChangeOwnPassword(scope, subject string, userID, platformUserID, tenantID uint, currentPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}
	if currentPassword == newPassword {
		return ErrPasswordUnchanged
	}
	hashedPassword, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	return model.Write(func(tx *gorm.DB) error {
		actorID := userID
		targetType := "tenant_user"
		actionScope := "tenant"
		actorRole := "user"

		switch {
		case scope == "platform":
			var user model.PlatformUser
			if err := tx.First(&user, platformUserID).Error; err != nil {
				return err
			}
			if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)) != nil {
				return ErrCurrentPasswordInvalid
			}
			if err := tx.Model(&user).Updates(map[string]interface{}{
				"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
			}).Error; err != nil {
				return err
			}
			actorID = platformUserID
			tenantID = 0
			targetType = "platform_user"
			actionScope = "platform"
			actorRole = user.Role
		case strings.HasPrefix(subject, "staff:"):
			var staff model.Staff
			if err := tx.Where("id = ? AND tenant_id = ?", userID, tenantID).First(&staff).Error; err != nil {
				return err
			}
			if bcrypt.CompareHashAndPassword([]byte(staff.Password), []byte(currentPassword)) != nil {
				return ErrCurrentPasswordInvalid
			}
			if err := tx.Model(&staff).Updates(map[string]interface{}{
				"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
			}).Error; err != nil {
				return err
			}
			targetType = "staff"
			actorRole = staff.Roles
		default:
			var user model.User
			if err := tx.Where("id = ? AND tenant_id = ?", userID, tenantID).First(&user).Error; err != nil {
				return err
			}
			if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)) != nil {
				return ErrCurrentPasswordInvalid
			}
			if err := tx.Model(&user).Updates(map[string]interface{}{
				"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
			}).Error; err != nil {
				return err
			}
			actorRole = user.Role
		}

		return recordAuditTx(tx, actorID, tenantID, actorRole, actionScope, "account.password.change", targetType, actorID, "账号本人修改密码", "{}", "{\"sessions_revoked\":true}")
	})
}
