package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommerceStorefrontContactImageStoreScopesAndRemovesUploads(t *testing.T) {
	directory := t.TempDir()
	store := CommerceStorefrontContactImageStore{Directory: directory, PublicBaseURL: "https://tickets.example.com/"}
	imageURL, err := store.Save(3, 9, commercePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	prefix := "https://tickets.example.com/api/v1/public/commerce-storefront-contact-images/3/9/"
	if !strings.HasPrefix(imageURL, prefix) || !strings.HasSuffix(imageURL, ".png") {
		t.Fatalf("contact image URL=%q", imageURL)
	}
	if err := store.ValidateOwnedURL(3, 9, imageURL); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateOwnedURL(3, 10, imageURL); err == nil {
		t.Fatal("accepted another channel account's contact image")
	}
	filename := strings.TrimPrefix(imageURL, prefix)
	path := filepath.Join(directory, "commerce-storefront-contacts", "3", "9", filename)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOwnedURL(3, 9, imageURL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("contact image still exists: %v", err)
	}
}

func TestCommerceStorefrontContactImageStoreRejectsInvalidUploads(t *testing.T) {
	store := CommerceStorefrontContactImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	if _, err := store.Save(1, 2, []byte("not an image")); err == nil {
		t.Fatal("accepted non-image contact upload")
	}
}
