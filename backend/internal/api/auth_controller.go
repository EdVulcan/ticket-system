package api

import (
	"errors"
	"net/http"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	Service service.AuthService
}

type PlatformLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginRequest struct {
	SystemCode string `json:"system_code" binding:"required"`
	Username   string `json:"username" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

func (c *AuthController) Login(ctx *gin.Context) {
	var req LoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, user, err := c.Service.Login(req.SystemCode, req.Username, req.Password)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "系统编号、账号或密码错误"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":                      user.ID,
			"tenant_id":               user.TenantID,
			"username":                user.Username,
			"role":                    user.Role,
			"is_initial_admin":        user.IsInitialAdmin,
			"system_code":             user.Tenant.SystemCode,
			"tenant_name":             user.Tenant.Name,
			"scope":                   "tenant",
			"capabilities":            user.Tenant.Capabilities,
			"supplier_business_types": user.Tenant.SupplierBusinessTypes,
			"permissions":             authz.PermissionsForRole(user.Role),
		},
	})
}

type StaffLoginRequest struct {
	SystemCode string `json:"system_code" binding:"required"`
	JobNumber  string `json:"job_number" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

func (c *AuthController) StaffLogin(ctx *gin.Context) {
	var req StaffLoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, staff, err := c.Service.StaffLogin(req.SystemCode, req.JobNumber, req.Password)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "系统编号、员工工号或密码错误"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"token": token,
		"staff": gin.H{
			"id":         staff.ID,
			"name":       staff.Name,
			"job_number": staff.JobNumber,
			"roles":      staff.Roles,
		},
	})
}

func (c *AuthController) PlatformLogin(ctx *gin.Context) {
	var req PlatformLoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	token, user, err := c.Service.PlatformLogin(req.Username, req.Password)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码错误"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": user.ID, "username": user.Username, "role": user.Role, "scope": "platform", "is_initial_admin": user.IsInitialAdmin}})
}

func (c *AuthController) ChangePassword(ctx *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写当前密码和新密码"})
		return
	}
	err := (&service.AccountService{}).ChangeOwnPassword(
		ctx.GetString("scope"), ctx.GetString("subject"), ctx.GetUint("user_id"), ctx.GetUint("platform_user_id"),
		ctx.GetUint("tenant_id"), req.CurrentPassword, req.NewPassword,
	)
	if err != nil {
		status := http.StatusBadRequest
		if !errors.Is(err, service.ErrCurrentPasswordInvalid) && !errors.Is(err, service.ErrPasswordUnchanged) && !errors.Is(err, service.ErrPasswordTooShort) {
			status = http.StatusInternalServerError
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "password changed"})
}
