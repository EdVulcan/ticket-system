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
	actorID := userID
	targetType := "tenant_user"
	actionScope := "tenant"
	actorRole := "user"
	currentHash := ""

	switch {
	case scope == "platform":
		var user model.PlatformUser
		if err := model.DB.First(&user, platformUserID).Error; err != nil {
			return err
		}
		currentHash = user.Password
		actorID = platformUserID
		tenantID = 0
		targetType = "platform_user"
		actionScope = "platform"
		actorRole = user.Role
	case strings.HasPrefix(subject, "staff:"):
		var staff model.Staff
		if err := model.DB.Where("id = ? AND tenant_id = ?", userID, tenantID).First(&staff).Error; err != nil {
			return err
		}
		currentHash = staff.Password
		targetType = "staff"
		actorRole = staff.Roles
	default:
		var user model.User
		if err := model.DB.Where("id = ? AND tenant_id = ?", userID, tenantID).First(&user).Error; err != nil {
			return err
		}
		currentHash = user.Password
		actorRole = user.Role
	}

	// Bcrypt work is intentionally outside the write transaction. Its cost is
	// high under the race detector and on small servers; holding the database
	// writer while checking or hashing a password can block unrelated sales.
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(currentPassword)) != nil {
		return ErrCurrentPasswordInvalid
	}
	hashedPassword, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	return model.Write(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
		}
		var result *gorm.DB
		switch targetType {
		case "platform_user":
			result = tx.Model(&model.PlatformUser{}).Where("id = ? AND password = ?", actorID, currentHash).Updates(updates)
		case "staff":
			result = tx.Model(&model.Staff{}).Where("id = ? AND tenant_id = ? AND password = ?", actorID, tenantID, currentHash).Updates(updates)
		default:
			result = tx.Model(&model.User{}).Where("id = ? AND tenant_id = ? AND password = ?", actorID, tenantID, currentHash).Updates(updates)
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCurrentPasswordInvalid
		}
		return recordAuditTx(tx, actorID, tenantID, actorRole, actionScope, "account.password.change", targetType, actorID, "账号本人修改密码", "{}", "{\"sessions_revoked\":true}")
	})
}
