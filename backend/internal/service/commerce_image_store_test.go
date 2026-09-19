package service

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commercePNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCommerceImageStoreSavesAndScopesMedia(t *testing.T) {
	directory := t.TempDir()
	store := CommerceImageStore{Directory: directory, PublicBaseURL: "https://tickets.example.com/"}
	data := commercePNG(t)

	imageURL, err := store.Save(3, 5, CommerceProductMediaCover, data)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "https://tickets.example.com/api/v1/public/commerce-product-images/3/5/cover/"
	if !strings.HasPrefix(imageURL, prefix) || !strings.HasSuffix(imageURL, ".png") {
		t.Fatalf("image URL = %q", imageURL)
	}
	filename := strings.TrimPrefix(imageURL, prefix)
	stored, err := os.ReadFile(filepath.Join(directory, "commerce-products", "3", "5", "cover", filename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, data) {
		t.Fatal("stored image differs from upload")
	}
	if err := store.ValidateOwnedURL(3, 5, CommerceProductMediaCover, imageURL); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		kind string
		url  string
	}{
		{CommerceProductMediaCover, strings.Replace(imageURL, "/3/5/", "/3/6/", 1)},
		{CommerceProductMediaDetail, imageURL},
		{CommerceProductMediaCover, imageURL + "?remote=1"},
	} {
		if err := store.ValidateOwnedURL(3, 5, invalid.kind, invalid.url); err == nil {
			t.Fatalf("accepted invalid media URL kind=%q url=%q", invalid.kind, invalid.url)
		}
	}
}

func TestCommerceImageStoreRejectsNonImageAndInvalidKind(t *testing.T) {
	store := CommerceImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	if _, err := store.Save(1, 2, "banner", commercePNG(t)); err == nil {
		t.Fatal("accepted invalid media kind")
	}
	if _, err := store.Save(1, 2, CommerceProductMediaDetail, []byte("not an image")); err == nil {
		t.Fatal("accepted non-image upload")
	}
}
