package service

import (
	"encoding/json"
	"errors"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

var (
	ErrPlatformUsernameExists = errors.New("平台用户名已存在")
	ErrInitialPlatformAdmin   = errors.New("初始平台管理员不能删除、停用或降权")
	ErrOwnPlatformAccount     = errors.New("不能删除、停用、降权或重置自己的平台账号")
)

type PlatformAccountService struct{}

func validPlatformRole(role string) bool {
	return role == "platform_admin" || role == "platform_operator"
}

func validPlatformUserStatus(status string) bool {
	return status == "active" || status == "frozen"
}

func (s *PlatformAccountService) List() ([]model.PlatformUser, error) {
	var users []model.PlatformUser
	err := model.DB.Order("is_initial_admin DESC, id ASC").Find(&users).Error
	return users, err
}

func (s *PlatformAccountService) Create(username, password, role string, actorID uint, actorRole string) (*model.PlatformUser, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 || !validPlatformRole(role) {
		return nil, errors.New("用户名、密码或角色无效")
	}
	hashedPassword, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := model.PlatformUser{Username: username, Password: hashedPassword, Role: role, Status: "active"}
	err = model.Write(func(tx *gorm.DB) error {
		var existing model.PlatformUser
		err := tx.Unscoped().Where("username = ?", username).First(&existing).Error
		if err == nil {
			if !existing.DeletedAt.Valid {
				return ErrPlatformUsernameExists
			}
			if err := tx.Unscoped().Model(&existing).Updates(map[string]interface{}{
				"password": hashedPassword, "role": role, "status": "active", "is_initial_admin": false,
				"token_version": existing.TokenVersion + 1, "deleted_at": nil,
			}).Error; err != nil {
				return err
			}
			user = existing
			user.Role = role
			user.Status = "active"
			user.IsInitialAdmin = false
			user.DeletedAt.Valid = false
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		after, _ := json.Marshal(map[string]interface{}{"username": username, "role": role, "status": "active"})
		return recordAuditTx(tx, actorID, 0, actorRole, "platform", "platform_user.create", "platform_user", user.ID, "新增平台账号", "{}", string(after))
	})
	return &user, err
}

func (s *PlatformAccountService) Update(id, actorID uint, actorRole, role, status string) error {
	if id == 0 || !validPlatformRole(role) || !validPlatformUserStatus(status) {
		return errors.New("平台账号参数无效")
	}
	return model.Write(func(tx *gorm.DB) error {
		var user model.PlatformUser
		if err := tx.First(&user, id).Error; err != nil {
			return err
		}
		if user.IsInitialAdmin && (role != "platform_admin" || status != "active") {
			return ErrInitialPlatformAdmin
		}
		if user.ID == actorID && (role != user.Role || status != "active") {
			return ErrOwnPlatformAccount
		}
		if user.Role == role && user.Status == status {
			return nil
		}
		before, _ := json.Marshal(map[string]interface{}{"role": user.Role, "status": user.Status})
		if err := tx.Model(&user).Updates(map[string]interface{}{
			"role": role, "status": status, "token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
			return err
		}
		after, _ := json.Marshal(map[string]interface{}{"role": role, "status": status})
		return recordAuditTx(tx, actorID, 0, actorRole, "platform", "platform_user.update", "platform_user", user.ID, "调整平台账号权限", string(before), string(after))
	})
}

func (s *PlatformAccountService) ResetPassword(id, actorID uint, actorRole, password string) error {
	if id == 0 || len(password) < 8 {
		return errors.New("新密码长度至少为8位")
	}
	if id == actorID {
		return ErrOwnPlatformAccount
	}
	hashedPassword, err := HashPassword(password)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var user model.PlatformUser
		if err := tx.First(&user, id).Error; err != nil {
			return err
		}
		if user.IsInitialAdmin {
			return ErrInitialPlatformAdmin
		}
		if err := tx.Model(&user).Updates(map[string]interface{}{
			"password": hashedPassword, "token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, 0, actorRole, "platform", "platform_user.password.reset", "platform_user", user.ID, "平台管理员重置子账号密码", "{}", "{\"sessions_revoked\":true}")
	})
}

func (s *PlatformAccountService) Delete(id, actorID uint, actorRole string) error {
	if id == 0 {
		return gorm.ErrRecordNotFound
	}
	if id == actorID {
		return ErrOwnPlatformAccount
	}
	return model.Write(func(tx *gorm.DB) error {
		var user model.PlatformUser
		if err := tx.First(&user, id).Error; err != nil {
			return err
		}
		if user.IsInitialAdmin {
			return ErrInitialPlatformAdmin
		}
		before, _ := json.Marshal(map[string]interface{}{"username": user.Username, "role": user.Role, "status": user.Status})
		if err := tx.Delete(&user).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, 0, actorRole, "platform", "platform_user.delete", "platform_user", user.ID, "删除平台子账号", string(before), "{\"deleted\":true}")
	})
}
