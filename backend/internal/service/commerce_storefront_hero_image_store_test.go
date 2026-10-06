package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommerceStorefrontHeroImageStoreScopesAndValidatesUploads(t *testing.T) {
	directory := t.TempDir()
	store := CommerceStorefrontHeroImageStore{Directory: directory, PublicBaseURL: "https://tickets.example.com/"}
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0dIDAT\x08\xd7c\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	imageURL, err := store.Save(3, 11, data)
	if err != nil {
		t.Fatalf("save hero image: %v", err)
	}
	if !strings.HasPrefix(imageURL, "https://tickets.example.com/api/v1/public/commerce-storefront-hero-images/3/11/") {
		t.Fatalf("unexpected hero image URL: %s", imageURL)
	}
	if err := store.ValidateOwnedURL(3, 11, imageURL); err != nil {
		t.Fatalf("validate saved hero image: %v", err)
	}
	if err := store.ValidateOwnedURL(4, 11, imageURL); err == nil {
		t.Fatal("cross-tenant hero image was accepted")
	}
	if err := store.RemoveOwnedURL(3, 11, imageURL); err != nil {
		t.Fatalf("remove hero image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "commerce-storefront-heroes")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("inspect hero directory: %v", err)
	}
}
