package api

import (
	"errors"
	"fmt"
	"net/http"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PaymentConfigController struct {
	Service service.PaymentService // Reuse PaymentService or create ConfigService
}

// GetConfigs 获取当前租户的所有支付配置
func (c *PaymentConfigController) GetConfigs(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")

	var configs []model.PaymentConfig
	if err := model.DB.Where("tenant_id = ?", tenantID).Find(&configs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Mask Secrets
	for i := range configs {
		if configs[i].Key != "" {
			configs[i].Key = "******"
		}
		if configs[i].PrivateKey != "" {
			configs[i].PrivateKey = "******"
		}
		if configs[i].PublicKey != "" {
			configs[i].PublicKey = "******"
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"data": configs})
}

// SaveConfig 保存或更新配置
func (c *PaymentConfigController) SaveConfig(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	var req model.PaymentConfig
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.TenantID = tenantID
	if req.Provider != "wechat" && req.Provider != "alipay" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "unsupported payment provider"})
		return
	}

	// Encrypt Secrets
	if req.Key != "" && req.Key != "******" {
		enc, err := utils.EncryptAES(req.Key)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("encrypt payment key: %v", err)})
			return
		}
		req.Key = enc
	}
	if req.PrivateKey != "" && req.PrivateKey != "******" {
		enc, err := utils.EncryptAES(req.PrivateKey)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("encrypt private key: %v", err)})
			return
		}
		req.PrivateKey = enc
	}
	if req.PublicKey != "" && req.PublicKey != "******" {
		enc, err := utils.EncryptAES(req.PublicKey)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("encrypt public key: %v", err)})
			return
		}
		req.PublicKey = enc
	}

	if err := model.Write(func(tx *gorm.DB) error {
		var existing model.PaymentConfig
		err := tx.Where("tenant_id = ? AND provider = ?", tenantID, req.Provider).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			req.Base = model.Base{}
			return tx.Create(&req).Error
		}
		if err != nil {
			return err
		}
		if req.Key == "******" || req.Key == "" {
			req.Key = existing.Key
		}
		if req.PrivateKey == "******" || req.PrivateKey == "" {
			req.PrivateKey = existing.PrivateKey
		}
		if req.PublicKey == "******" || req.PublicKey == "" {
			req.PublicKey = existing.PublicKey
		}
		req.ID = existing.ID
		return tx.Model(&existing).Updates(map[string]interface{}{
			"app_id": req.AppID, "mch_id": req.MchID, "key": req.Key,
			"private_key": req.PrivateKey, "public_key": req.PublicKey,
			"serial_no": req.SerialNo, "notify_url": req.NotifyURL, "status": req.Status,
		}).Error
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	req.Key = "******"
	req.PrivateKey = "******"
	req.PublicKey = "******"
	ctx.JSON(http.StatusOK, req)
}
