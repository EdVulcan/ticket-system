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

func savedSecretMarker(value string) string {
	if value != "" {
		return "******"
	}
	return ""
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
		if configs[i].PlatformPublicKey != "" {
			configs[i].PlatformPublicKey = "******"
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"data": configs})
}

func (c *PaymentConfigController) GetReadiness(ctx *gin.Context) {
	items, err := c.Service.GetConfigReadiness(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": items})
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
	if req.Status {
		var existing model.PaymentConfig
		err := model.DB.Where("tenant_id = ? AND provider = ?", tenantID, req.Provider).First(&existing).Error
		candidate := req
		if err == nil {
			if candidate.Key == "" || candidate.Key == "******" {
				candidate.Key = savedSecretMarker(existing.Key)
			}
			if candidate.PrivateKey == "" || candidate.PrivateKey == "******" {
				candidate.PrivateKey = savedSecretMarker(existing.PrivateKey)
			}
			if candidate.PublicKey == "" || candidate.PublicKey == "******" {
				candidate.PublicKey = savedSecretMarker(existing.PublicKey)
			}
			if candidate.PlatformPublicKey == "" || candidate.PlatformPublicKey == "******" {
				candidate.PlatformPublicKey = savedSecretMarker(existing.PlatformPublicKey)
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if issues := service.PaymentConfigIssues(&candidate, tenantID); len(issues) > 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "支付配置尚未完整，不能启用", "issues": issues})
			return
		}
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
	if req.PlatformPublicKey != "" && req.PlatformPublicKey != "******" {
		enc, err := utils.EncryptAES(req.PlatformPublicKey)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("encrypt payment platform public key: %v", err)})
			return
		}
		req.PlatformPublicKey = enc
	}

	if err := model.Write(func(tx *gorm.DB) error {
		var existing model.PaymentConfig
		err := tx.Where("tenant_id = ? AND provider = ?", tenantID, req.Provider).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			req.Base = model.Base{}
			return tx.Select("*").Create(&req).Error
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
		if req.PlatformPublicKey == "******" || req.PlatformPublicKey == "" {
			req.PlatformPublicKey = existing.PlatformPublicKey
		}
		req.ID = existing.ID
		return tx.Model(&existing).Updates(map[string]interface{}{
			"app_id": req.AppID, "mch_id": req.MchID, "key": req.Key,
			"private_key": req.PrivateKey, "public_key": req.PublicKey,
			"serial_no": req.SerialNo, "notify_url": req.NotifyURL, "status": req.Status,
			"platform_public_key": req.PlatformPublicKey, "platform_public_key_id": req.PlatformPublicKeyID,
		}).Error
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	req.Key = "******"
	req.PrivateKey = "******"
	req.PublicKey = "******"
	req.PlatformPublicKey = "******"
	ctx.JSON(http.StatusOK, req)
}
