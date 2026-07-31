package api

import (
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type StaffController struct{}

// Create Staff
func (c *StaffController) Create(ctx *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		JobNumber string `json:"job_number" binding:"required"`
		Password  string `json:"password" binding:"required"`
		Roles     string `json:"roles"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Password) < 8 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
		return
	}
	for _, role := range strings.Split(req.Roles, ",") {
		role = strings.TrimSpace(role)
		if role != "" && role != "seller" && role != "checker" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid staff role"})
			return
		}
	}

	// Hash password
	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Set TenantID from context
	tenantID := ctx.GetUint("tenant_id")

	staff := model.Staff{
		Name:      req.Name,
		JobNumber: req.JobNumber,
		Password:  hashedPwd,
		Roles:     req.Roles,
		Status:    "active",
		TenantID:  tenantID,
	}
	if staff.Roles == "" {
		staff.Roles = "seller"
	}

	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&staff).Error }); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, staff)
}

// List Staff
func (c *StaffController) List(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	var staffs []model.Staff
	if err := model.DB.Preload("ResourceScopes").Where("tenant_id = ?", tenantID).Find(&staffs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, staffs)
}

func (c *StaffController) SetResourceScopes(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid staff id"})
		return
	}
	var body struct {
		Scopes []model.StaffResourceScope `json:"scopes"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ReplaceStaffResourceScopes(ctx.GetUint("tenant_id"), uint(id), body.Scopes); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "resource scopes updated"})
}

// Delete Staff
func (c *StaffController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var rowsAffected int64
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, ctx.GetUint("tenant_id")).Delete(&model.Staff{})
		rowsAffected = result.RowsAffected
		return result.Error
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rowsAffected == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "staff not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

// Reset Password
func (c *StaffController) ResetPassword(ctx *gin.Context) {
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
		result := tx.Model(&model.Staff{}).Where("id = ? AND tenant_id = ?", id, ctx.GetUint("tenant_id")).Updates(map[string]interface{}{"password": hashedPwd, "token_version": gorm.Expr("token_version + 1")})
		rowsAffected = result.RowsAffected
		return result.Error
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rowsAffected == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "staff not found"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
