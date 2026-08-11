package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

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
