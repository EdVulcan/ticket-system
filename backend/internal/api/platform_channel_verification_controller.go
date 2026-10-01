package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PlatformChannelVerificationController struct {
	Service service.PlatformChannelVerificationService
}

func (c *PlatformChannelVerificationController) Accounts(ctx *gin.Context) {
	rows, err := c.Service.Accounts()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "读取渠道账号失败"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *PlatformChannelVerificationController) List(ctx *gin.Context) {
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	accountID, _ := strconv.ParseUint(ctx.Query("channel_account_id"), 10, 32)
	rows, err := c.Service.List(uint(tenantID), uint(accountID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "读取校验文件失败"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *PlatformChannelVerificationController) Upload(ctx *gin.Context) {
	tenantID, err := strconv.ParseUint(ctx.PostForm("tenant_id"), 10, 32)
	if err != nil || tenantID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择租户"})
		return
	}
	accountID, err := strconv.ParseUint(ctx.PostForm("channel_account_id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择渠道账号"})
		return
	}
	fileHeader, err := ctx.FormFile("file")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择校验文件"})
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > service.MaxChannelVerificationFileBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "校验文件大小无效"})
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "读取校验文件失败"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxChannelVerificationFileBytes+1))
	if err != nil || len(data) > service.MaxChannelVerificationFileBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "读取校验文件失败"})
		return
	}

	row, err := c.Service.Upload(ctx.GetUint("platform_user_id"), uint(tenantID), uint(accountID), ctx.PostForm("kind"), fileHeader.Filename, data)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, service.ErrVerificationFileConflict) {
			status = http.StatusConflict
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *PlatformChannelVerificationController) Delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "校验文件编号无效"})
		return
	}
	if err := c.Service.Delete(ctx.GetUint("platform_user_id"), uint(id)); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrVerificationFileNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.Status(http.StatusNoContent)
}
