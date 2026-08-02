package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PlatformUserController struct {
	Service service.PlatformAccountService
}

func (c *PlatformUserController) List(ctx *gin.Context) {
	users, err := c.Service.List()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, users)
}

func (c *PlatformUserController) Create(ctx *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写用户名、密码和角色"})
		return
	}
	user, err := c.Service.Create(req.Username, req.Password, req.Role, ctx.GetUint("platform_user_id"), ctx.GetString("role"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrPlatformUsernameExists) {
			status = http.StatusConflict
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, user)
}

func (c *PlatformUserController) Update(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "平台账号编号无效"})
		return
	}
	var req struct {
		Role   string `json:"role" binding:"required"`
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写角色和状态"})
		return
	}
	if err := c.Service.Update(uint(id), ctx.GetUint("platform_user_id"), ctx.GetString("role"), req.Role, req.Status); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "platform user updated"})
}

func (c *PlatformUserController) ResetPassword(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "平台账号编号无效"})
		return
	}
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写新密码"})
		return
	}
	if err := c.Service.ResetPassword(uint(id), ctx.GetUint("platform_user_id"), ctx.GetString("role"), req.Password); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "password reset"})
}

func (c *PlatformUserController) Delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "平台账号编号无效"})
		return
	}
	if err := c.Service.Delete(uint(id), ctx.GetUint("platform_user_id"), ctx.GetString("role")); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "platform user deleted"})
}
