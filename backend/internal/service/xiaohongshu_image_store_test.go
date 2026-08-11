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

func TestXiaohongshuImageStoreSavesValidatedImage(t *testing.T) {
	directory := t.TempDir()
	store := XiaohongshuImageStore{Directory: directory, PublicBaseURL: "https://tickets.example.com/"}

	var imageData bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, canvas); err != nil {
		t.Fatal(err)
	}

	imageURL, err := store.Save(3, 5, imageData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	prefix := "https://tickets.example.com/media/channel-products/3/5/"
	if !strings.HasPrefix(imageURL, prefix) || !strings.HasSuffix(imageURL, ".png") {
		t.Fatalf("image URL = %q", imageURL)
	}
	filename := strings.TrimPrefix(imageURL, prefix)
	stored, err := os.ReadFile(filepath.Join(directory, "channel-products", "3", "5", filename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, imageData.Bytes()) {
		t.Fatal("stored image differs from upload")
	}
}

func TestXiaohongshuImageStoreRejectsNonImage(t *testing.T) {
	store := XiaohongshuImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	if _, err := store.Save(1, 2, []byte("not an image")); err == nil {
		t.Fatal("expected non-image upload to be rejected")
	}
}
