package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	Service service.AuthService
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
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
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
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
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
