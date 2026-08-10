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
	if response.Body.String() != "74e84f27de41f119d9\n" {
		t.Fatalf("body = %q", response.Body.String())
	}
}
