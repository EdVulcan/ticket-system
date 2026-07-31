package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errUsernameExists = errors.New("username already exists")

type UserController struct{}

// Create System User
func (c *UserController) Create(ctx *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role" binding:"required"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Password) < 8 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
		return
	}
	if req.Role != "admin" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid user role"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	// Hash password
	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Set TenantID from context (Admin creates staff for their tenant)
	tenantID := ctx.GetUint("tenant_id")

	user := model.User{
		Username: req.Username,
		Password: hashedPwd,
		Role:     req.Role,
		TenantID: tenantID,
	}

	if err := model.Write(func(tx *gorm.DB) error {
		var existing model.User
		err := tx.Unscoped().Where("username = ? AND tenant_id = ?", req.Username, tenantID).First(&existing).Error
		if err == nil {
			if !existing.DeletedAt.Valid {
				return errUsernameExists
			}
			if err := tx.Unscoped().Model(&existing).Updates(map[string]interface{}{
				"password": hashedPwd, "role": req.Role, "deleted_at": nil,
			}).Error; err != nil {
				return err
			}
			user = existing
			user.Password = hashedPwd
			user.Role = req.Role
			user.DeletedAt.Valid = false
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&user).Error
	}); err != nil {
		if errors.Is(err, errUsernameExists) {
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, user)
}

// List Staff
func (c *UserController) List(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	var users []model.User
	if err := model.DB.Where("tenant_id = ?", tenantID).Find(&users).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, users)
}

// Delete System User
func (c *UserController) Delete(ctx *gin.Context) {
	currentUserID := ctx.GetUint("user_id")
	currentUserRole := ctx.GetString("role")

	// 1. Permission Check: Only 'admin' or 'super_admin' can delete users
	if currentUserRole != "admin" && currentUserRole != "super_admin" {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: Only main administrator can delete users"})
		return
	}

	// 2. Self-Delete Check
	id, _ := strconv.Atoi(ctx.Param("id"))
	if uint(id) == currentUserID {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete your own account"})
		return
	}

	// 3. Soft delete while preserving the account's audit identity.
	tenantID := ctx.GetUint("tenant_id")
	var rowsAffected int64
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.User{})
		rowsAffected = result.RowsAffected
		return result.Error
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rowsAffected == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

// Reset Password
func (c *UserController) ResetPassword(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var req struct {
		Password string `json:"password"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Password) < 8 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
		return
	}

	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	var rowsAffected int64
	err = model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).Where("id = ? AND tenant_id = ?", id, ctx.GetUint("tenant_id")).Updates(map[string]interface{}{"password": hashedPwd, "token_version": gorm.Expr("token_version + 1")})
		rowsAffected = result.RowsAffected
		return result.Error
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rowsAffected == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
