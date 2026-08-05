package service

import (
	"fmt"
	"net/url"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type PaymentCapabilityReadiness struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
}

type PaymentProviderReadiness struct {
	Provider           string                       `json:"provider"`
	Name               string                       `json:"name"`
	Configured         bool                         `json:"configured"`
	Enabled            bool                         `json:"enabled"`
	ConfigurationReady bool                         `json:"configuration_ready"`
	CallbackURL        string                       `json:"callback_url,omitempty"`
	Issues             []string                     `json:"issues"`
	Capabilities       []PaymentCapabilityReadiness `json:"capabilities"`
}

func PaymentConfigIssues(cfg *model.PaymentConfig, tenantID uint) []string {
	issues := make([]string, 0)
	require := func(value, message string) {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, message)
		}
	}
	require(cfg.AppID, "缺少应用编号")
	switch cfg.Provider {
	case "wechat":
		require(cfg.MchID, "缺少微信支付商户号")
		require(cfg.Key, "缺少 API v3 密钥")
		if key := strings.TrimSpace(cfg.Key); key != "" && key != "******" && len(key) != 32 {
			issues = append(issues, "API v3 密钥必须为 32 个字符")
		}
		require(cfg.SerialNo, "缺少商户证书序列号")
		require(cfg.PrivateKey, "缺少商户接口私钥")
		require(cfg.PlatformPublicKeyID, "缺少微信支付平台公钥编号")
		require(cfg.PlatformPublicKey, "缺少微信支付平台公钥")
	case "alipay":
		require(cfg.PrivateKey, "缺少支付宝应用私钥")
		require(cfg.PublicKey, "缺少支付宝公钥")
	default:
		issues = append(issues, "不支持的支付渠道")
	}
	require(cfg.NotifyURL, "缺少支付结果通知地址")
	if strings.TrimSpace(cfg.NotifyURL) != "" {
		parsed, err := url.Parse(cfg.NotifyURL)
		expectedSuffix := fmt.Sprintf("/api/v1/payments/notify/%s/%d", cfg.Provider, tenantID)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			issues = append(issues, "通知地址必须是公网可访问的 HTTPS 地址")
		} else if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), expectedSuffix) {
			issues = append(issues, "通知地址路径应以 "+expectedSuffix+" 结尾")
		}
	}
	return issues
}

func (s *PaymentService) GetConfigReadiness(tenantID uint) ([]PaymentProviderReadiness, error) {
	result := make([]PaymentProviderReadiness, 0, 2)
	for _, provider := range []string{"wechat", "alipay"} {
		item := PaymentProviderReadiness{Provider: provider, Name: map[string]string{"wechat": "微信支付", "alipay": "支付宝"}[provider], Issues: []string{}}
		var cfg model.PaymentConfig
		err := model.DB.Where("tenant_id = ? AND provider = ?", tenantID, provider).First(&cfg).Error
		if err != nil {
			if err != gorm.ErrRecordNotFound {
				return nil, err
			}
			item.Issues = []string{"尚未保存配置"}
			item.Capabilities = paymentCapabilities(provider, false, false, nil)
			result = append(result, item)
			continue
		}
		item.Configured = true
		item.Enabled = cfg.Status
		item.CallbackURL = cfg.NotifyURL
		if err := decryptPaymentConfig(&cfg); err != nil {
			item.Issues = []string{"密钥无法解密，请重新保存配置"}
			item.Capabilities = paymentCapabilities(provider, false, false, nil)
			result = append(result, item)
			continue
		}
		item.Issues = PaymentConfigIssues(&cfg, tenantID)
		baseReady := paymentBaseReady(&cfg)
		callbackReady := len(item.Issues) == 0
		item.ConfigurationReady = item.Enabled && callbackReady
		item.Capabilities = paymentCapabilities(provider, item.Enabled && baseReady, item.Enabled && callbackReady, &cfg)
		result = append(result, item)
	}
	return result, nil
}

func paymentBaseReady(cfg *model.PaymentConfig) bool {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.PrivateKey) == "" {
		return false
	}
	if cfg.Provider == "wechat" {
		return strings.TrimSpace(cfg.MchID) != "" && len(strings.TrimSpace(cfg.Key)) == 32 && strings.TrimSpace(cfg.SerialNo) != ""
	}
	return cfg.Provider == "alipay" && strings.TrimSpace(cfg.PublicKey) != ""
}

func paymentCapabilities(provider string, baseReady, callbackReady bool, cfg *model.PaymentConfig) []PaymentCapabilityReadiness {
	items := []PaymentCapabilityReadiness{
		{Code: "customer_scan", Name: "顾客扫码支付", Available: baseReady},
		{Code: "payment_code", Name: "付款码收款", Available: baseReady},
		{Code: "query", Name: "主动查单", Available: baseReady},
		{Code: "refund", Name: "原路退款", Available: baseReady},
		{Code: "callback", Name: "支付结果通知", Available: callbackReady},
	}
	if provider == "wechat" {
		items[1].Available = baseReady && cfg != nil && len(strings.TrimSpace(cfg.WechatV2Key)) == 32 && strings.TrimSpace(cfg.MerchantCertificate) != ""
		switch {
		case cfg == nil || strings.TrimSpace(cfg.WechatV2Key) == "":
			items[1].Note = "缺少 API v2 密钥"
		case len(strings.TrimSpace(cfg.WechatV2Key)) != 32:
			items[1].Note = "API v2 密钥必须为 32 个字符"
		case strings.TrimSpace(cfg.MerchantCertificate) == "":
			items[1].Note = "请重新上传商户证书和私钥"
		default:
			items[1].Note = "已具备配置，仍需真实商户小额联调"
		}
	}
	return items
}
