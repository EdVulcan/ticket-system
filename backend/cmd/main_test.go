package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	engine := gin.New()
	serveAdminUI(engine, directory)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/74e84f27.txt", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "74e84f27de41f119d9" {
		t.Fatalf("body = %q", response.Body.String())
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
	servePublicUploads(engine, directory)
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
