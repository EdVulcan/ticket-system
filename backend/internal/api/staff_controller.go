package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
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

	if err := model.DB.Create(&staff).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, staff)
}

// List Staff
func (c *StaffController) List(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	var staffs []model.Staff
	if err := model.DB.Where("tenant_id = ?", tenantID).Find(&staffs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, staffs)
}

// Delete Staff
func (c *StaffController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := model.DB.Delete(&model.Staff{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	hashedPwd, err := service.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := model.DB.Model(&model.Staff{}).Where("id = ?", id).Update("password", hashedPwd).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
