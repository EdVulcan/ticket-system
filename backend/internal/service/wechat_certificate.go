package service

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

type WechatMerchantCertificate struct {
	MerchantID  string
	SerialNo    string
	ExpiresAt   time.Time
	Fingerprint string
	PrivateKey  string
}

func ParseWechatMerchantCertificate(certPEM, privateKeyPEM []byte, now time.Time) (*WechatMerchantCertificate, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("商户证书不是有效的 PEM 证书")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析商户证书失败: %w", err)
	}
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return nil, fmt.Errorf("商户证书不在有效期内")
	}
	merchantID := strings.TrimSpace(cert.Subject.CommonName)
	if merchantID == "" {
		return nil, fmt.Errorf("商户证书中缺少商户号")
	}

	keyBlock, _ := pem.Decode(privateKeyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("商户私钥不是有效的 PEM 私钥")
	}
	privateKey, err := parseWechatRSAPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.Cmp(privateKey.PublicKey.N) != 0 || publicKey.E != privateKey.PublicKey.E {
		return nil, fmt.Errorf("商户证书与商户私钥不匹配")
	}

	fingerprint := sha256.Sum256(cert.Raw)
	return &WechatMerchantCertificate{
		MerchantID:  merchantID,
		SerialNo:    strings.ToUpper(cert.SerialNumber.Text(16)),
		ExpiresAt:   cert.NotAfter,
		Fingerprint: strings.ToUpper(hex.EncodeToString(fingerprint[:])),
		PrivateKey:  strings.TrimSpace(string(privateKeyPEM)),
	}, nil
}

func parseWechatRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("商户私钥不是 RSA 私钥")
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("无法解析商户私钥")
}

func ValidateWechatPlatformPublicKey(value []byte) error {
	if _, err := parseRSAPublicKey(string(value)); err != nil {
		return fmt.Errorf("微信支付平台公钥无效: %w", err)
	}
	return nil
}
