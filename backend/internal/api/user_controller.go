package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

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

	// Hash password
	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Set TenantID from context (Admin creates staff for their tenant)
	tenantID := ctx.GetUint("tenant_id")

	// Check for existing soft-deleted user and hard delete it to allow reuse
	var existingUser model.User
	if err := model.DB.Unscoped().Where("username = ? AND tenant_id = ?", req.Username, tenantID).First(&existingUser).Error; err == nil {
		if existingUser.DeletedAt.Valid {
			model.DB.Unscoped().Delete(&existingUser)
		}
	}

	user := model.User{
		Username: req.Username,
		Password: hashedPwd,
		Role:     req.Role,
		TenantID: tenantID,
	}

	if err := model.DB.Create(&user).Error; err != nil {
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

	// 3. Perform Hard Delete (Unscoped) to free up the username
	if err := model.DB.Unscoped().Delete(&model.User{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := model.DB.Model(&model.User{}).Where("id = ?", id).Update("password", hashedPwd).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
