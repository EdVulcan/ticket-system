package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"ticket-backend/internal/utils"

	"github.com/gin-gonic/gin"
)

func TestSaveWechatConfigEncryptsUploadedPrivateKey(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.PaymentConfig{}); err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	oldEncryptionKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = strings.Repeat("e", 32)
	t.Cleanup(func() {
		config.GlobalConfig.Security.EncryptionKey = oldEncryptionKey
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = nil
	})

	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(0x1234),
		Subject:      pkix.Name{CommonName: "1602588787"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	platformPublicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	platformPublicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: platformPublicKeyDER})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"app_id":                 "wx-test",
		"key":                    "0123456789abcdef0123456789abcdef",
		"platform_public_key_id": "PUB_KEY_ID_123",
		"status":                 "false",
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	certPart, _ := writer.CreateFormFile("merchant_certificate", "apiclient_cert.pem")
	_, _ = certPart.Write(certPEM)
	keyPart, _ := writer.CreateFormFile("merchant_private_key", "apiclient_key.pem")
	_, _ = keyPart.Write(keyPEM)
	platformKeyPart, _ := writer.CreateFormFile("platform_public_key_file", "wechatpay_public_key.pem")
	_, _ = platformKeyPart.Write(platformPublicKeyPEM)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", "/api/v1/payments/configs/wechat", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Set("tenant_id", uint(7))
	(&PaymentConfigController{}).SaveWechatConfig(ctx)
	if response.Code != 200 {
		t.Fatalf("save response=%d body=%s", response.Code, response.Body.String())
	}

	var stored model.PaymentConfig
	if err := db.Where("tenant_id = ? AND provider = ?", 7, "wechat").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MchID != "1602588787" || stored.SerialNo != "1234" || stored.PrivateKey == "" || strings.Contains(stored.PrivateKey, "PRIVATE KEY") {
		t.Fatalf("unexpected stored config: mch=%s serial=%s private_plaintext=%v", stored.MchID, stored.SerialNo, strings.Contains(stored.PrivateKey, "PRIVATE KEY"))
	}
	if stored.Key == "0123456789abcdef0123456789abcdef" || strings.Contains(stored.PlatformPublicKey, "PUBLIC KEY") {
		t.Fatal("payment secrets were stored as plaintext")
	}
	decrypted, err := utils.DecryptAES(stored.PrivateKey)
	if err != nil || decrypted != strings.TrimSpace(string(keyPEM)) {
		t.Fatalf("stored private key did not decrypt correctly: %v", err)
	}
	decryptedAPIKey, err := utils.DecryptAES(stored.Key)
	if err != nil || decryptedAPIKey != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("stored API v3 key did not decrypt correctly: %v", err)
	}
	decryptedPlatformKey, err := utils.DecryptAES(stored.PlatformPublicKey)
	if err != nil || decryptedPlatformKey != strings.TrimSpace(string(platformPublicKeyPEM)) {
		t.Fatalf("stored platform public key did not decrypt correctly: %v", err)
	}

	getResponse := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getResponse)
	getContext.Set("tenant_id", uint(7))
	(&PaymentConfigController{}).GetConfigs(getContext)
	if strings.Contains(getResponse.Body.String(), "PRIVATE KEY") ||
		!strings.Contains(getResponse.Body.String(), `"key":"******"`) ||
		!strings.Contains(getResponse.Body.String(), `"private_key":"******"`) ||
		!strings.Contains(getResponse.Body.String(), `"platform_public_key":"******"`) {
		t.Fatalf("config response exposed or failed to mask payment secrets: %s", getResponse.Body.String())
	}
}
