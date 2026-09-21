package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestSeedAdminUserTxCreatesScenicBootstrapSupplier(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantCapability{}, &model.SupplierBusinessType{},
		&model.User{}, &model.PlatformUser{},
	); err != nil {
		t.Fatal(err)
	}
	bootstrap := config.BootstrapConfig{
		TenantName: "Bootstrap Scenic", SystemCode: "BOOTSTRAP-SCENIC",
		AdminUsername: "tenant-admin", AdminPassword: "tenant-bootstrap-password",
		PlatformUsername: "platform-admin", PlatformPassword: "platform-bootstrap-password",
	}
	seed := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return seedAdminUserTx(tx, bootstrap) })
	}
	if err := seed(); err != nil {
		t.Fatal(err)
	}
	if err := seed(); err != nil {
		t.Fatalf("repeat bootstrap seed: %v", err)
	}

	var tenant model.Tenant
	if err := db.Where("system_code = ?", bootstrap.SystemCode).First(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	var capability model.TenantCapability
	if err := db.Where("tenant_id = ? AND capability = ?", tenant.ID, "supplier").First(&capability).Error; err != nil {
		t.Fatal(err)
	}
	if capability.Status != "active" {
		t.Fatalf("bootstrap supplier capability=%q, want active", capability.Status)
	}
	var businessType model.SupplierBusinessType
	if err := db.Where("tenant_id = ? AND business_type = ?", tenant.ID, "scenic").First(&businessType).Error; err != nil {
		t.Fatal(err)
	}
	if businessType.Status != "active" || businessType.ActivatedAt == nil {
		t.Fatalf("bootstrap scenic business type=%+v", businessType)
	}
	for name, target := range map[string]interface{}{
		"tenant": &model.Tenant{}, "tenant user": &model.User{}, "platform user": &model.PlatformUser{},
		"supplier capability": &model.TenantCapability{}, "supplier business type": &model.SupplierBusinessType{},
	} {
		var count int64
		if err := db.Model(target).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s rows=%d, want 1", name, count)
		}
	}
}

func TestServeAdminUIExposesXiaohongshuValidationFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("admin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "74e84f27.txt"), []byte("74e84f27de41f119d9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "6cc8262d.txt"), []byte("6cc8262d80a22c265e\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "not-a-validation.txt"), []byte("must not be exposed"), 0o600); err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	serveAdminUI(engine, directory)
	for path, want := range map[string]string{
		"/74e84f27.txt": "74e84f27de41f119d9",
		"/6cc8262d.txt": "6cc8262d80a22c265e",
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusOK)
		}
		if response.Body.String() != want {
			t.Fatalf("%s body = %q", path, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
	}
	invalid := httptest.NewRecorder()
	engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/not-a-validation.txt", nil))
	if invalid.Code != http.StatusOK || invalid.Body.String() != "admin" {
		t.Fatalf("unexpected non-validation response: status=%d body=%q", invalid.Code, invalid.Body.String())
	}
}

func TestServePublicUploadsExposesOnlyValidatedProductImagePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	directory := t.TempDir()
	imageDirectory := filepath.Join(directory, "channel-products", "3", "5")
	if err := os.MkdirAll(imageDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	filename := "0123456789abcdef0123456789abcdef.png"
	if err := os.WriteFile(filepath.Join(imageDirectory, filename), []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	servePublicUploads(engine, directory, nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/channel-product-images/3/5/"+filename, nil))
	if response.Code != http.StatusOK || response.Body.String() != "image" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}

	invalid := httptest.NewRecorder()
	engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/public/channel-product-images/3/5/not-an-upload.png", nil))
	if invalid.Code != http.StatusNotFound {
		t.Fatalf("invalid filename status=%d", invalid.Code)
	}
}

func TestServePublicUploadsExposesCommerceProductImagePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	directory := t.TempDir()
	imageDirectory := filepath.Join(directory, "commerce-products", "3", "5", "detail")
	if err := os.MkdirAll(imageDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	filename := "fedcba9876543210fedcba9876543210.jpg"
	if err := os.WriteFile(filepath.Join(imageDirectory, filename), []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	servePublicUploads(engine, directory, nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/commerce-product-images/3/5/detail/"+filename, nil))
	if response.Code != http.StatusOK || response.Body.String() != "image" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"/api/v1/public/commerce-product-images/3/5/banner/" + filename,
		"/api/v1/public/commerce-product-images/3/5/detail/not-an-upload.jpg",
	} {
		invalid := httptest.NewRecorder()
		engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, path, nil))
		if invalid.Code != http.StatusNotFound {
			t.Fatalf("invalid path %s status=%d", path, invalid.Code)
		}
	}
}

func TestServePublicUploadsExposesCommerceStorefrontContactImagePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	directory := t.TempDir()
	filename := "abcdef0123456789abcdef0123456789.png"

	engine := gin.New()
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.ChannelAccount{}); err != nil {
		t.Fatal(err)
	}
	tenant := model.Tenant{Name: "Contact image tenant", SystemCode: "CONTACT-IMAGE", SecretKey: "contact-secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{TenantID: tenant.ID, Code: "contact-account", Type: "wechat_miniapp", Status: "active", Environment: "production"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	imageDirectory := filepath.Join(directory, "commerce-storefront-contacts", strconv.FormatUint(uint64(tenant.ID), 10), strconv.FormatUint(uint64(account.ID), 10))
	if err := os.MkdirAll(imageDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageDirectory, filename), []byte("contact-image"), 0o640); err != nil {
		t.Fatal(err)
	}
	contactURL := "https://tickets.example.com/api/v1/public/commerce-storefront-contact-images/" + strconv.FormatUint(uint64(tenant.ID), 10) + "/" + strconv.FormatUint(uint64(account.ID), 10) + "/" + filename
	if err := db.Model(&account).Updates(map[string]interface{}{"storefront_contact_status": "active", "storefront_contact_qr_code_url": contactURL}).Error; err != nil {
		t.Fatal(err)
	}
	servePublicUploads(engine, directory, db)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, contactURL, nil))
	if response.Code != http.StatusOK || response.Body.String() != "contact-image" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("contact image Cache-Control=%q", got)
	}
	newFilename := "fedcba9876543210fedcba9876543210.jpg"
	if err := os.WriteFile(filepath.Join(imageDirectory, newFilename), []byte("replacement-contact-image"), 0o640); err != nil {
		t.Fatal(err)
	}
	newContactURL := "https://tickets.example.com/api/v1/public/commerce-storefront-contact-images/" + strconv.FormatUint(uint64(tenant.ID), 10) + "/" + strconv.FormatUint(uint64(account.ID), 10) + "/" + newFilename
	if err := db.Model(&account).Updates(map[string]interface{}{"storefront_contact_qr_code_url": newContactURL}).Error; err != nil {
		t.Fatal(err)
	}
	oldAfterReplacement := httptest.NewRecorder()
	engine.ServeHTTP(oldAfterReplacement, httptest.NewRequest(http.MethodGet, contactURL, nil))
	if oldAfterReplacement.Code != http.StatusNotFound {
		t.Fatalf("old contact image after replacement status=%d", oldAfterReplacement.Code)
	}
	newResponse := httptest.NewRecorder()
	engine.ServeHTTP(newResponse, httptest.NewRequest(http.MethodGet, newContactURL, nil))
	if newResponse.Code != http.StatusOK || newResponse.Body.String() != "replacement-contact-image" {
		t.Fatalf("replacement contact image status=%d body=%q", newResponse.Code, newResponse.Body.String())
	}
	if err := db.Model(&account).Updates(map[string]interface{}{"storefront_contact_status": "disabled"}).Error; err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRecorder()
	engine.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, contactURL, nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled contact image status=%d", disabled.Code)
	}
	if err := db.Model(&account).Updates(map[string]interface{}{"storefront_contact_status": "active", "status": "disabled"}).Error; err != nil {
		t.Fatal(err)
	}
	accountDisabled := httptest.NewRecorder()
	engine.ServeHTTP(accountDisabled, httptest.NewRequest(http.MethodGet, contactURL, nil))
	if accountDisabled.Code != http.StatusNotFound {
		t.Fatalf("disabled channel contact image status=%d", accountDisabled.Code)
	}
	for _, path := range []string{
		"https://tickets.example.com/api/v1/public/commerce-storefront-contact-images/not-a-tenant/9/" + filename,
		"/api/v1/public/commerce-storefront-contact-images/3/9/not-an-upload.png",
	} {
		invalid := httptest.NewRecorder()
		requestURL := path
		if !strings.HasPrefix(path, "http") {
			requestURL = "https://tickets.example.com" + path
		}
		engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, requestURL, nil))
		if invalid.Code != http.StatusNotFound {
			t.Fatalf("invalid path %s status=%d", path, invalid.Code)
		}
	}
}
