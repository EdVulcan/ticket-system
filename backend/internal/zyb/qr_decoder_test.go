package zyb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestQRCodeDecoderPreservesSupplierPayload(t *testing.T) {
	const payload = "SUPPLIER-opaque-000123"
	matrix, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, 240, 240, nil)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 240, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 240; x++ {
			c := color.Gray{Y: 255}
			if matrix.Get(x, y) {
				c.Y = 0
			}
			img.SetGray(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := (QRCodeDecoder{}).Decode(context.Background(), encoded.Bytes())
	if err != nil || decoded != payload {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}
	if _, err = (QRCodeDecoder{}).Decode(context.Background(), []byte("not an image")); err == nil {
		t.Fatal("invalid image accepted")
	}
	encoded.Reset()
	if err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if decoded, err = (QRCodeDecoder{}).Decode(context.Background(), encoded.Bytes()); err != nil || decoded != payload {
		t.Fatalf("JPEG decode failed: %v", err)
	}
}

func TestQRCodeDecoderDecodeAllReturnsDistinctPayloadsDeterministically(t *testing.T) {
	data := encodeQRCodeSheet(t, []string{
		"SUPPLIER-opaque-000002",
		"SUPPLIER-opaque-000001",
		"SUPPLIER-opaque-000002",
	})

	codes, err := (QRCodeDecoder{}).DecodeAll(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SUPPLIER-opaque-000001", "SUPPLIER-opaque-000002"}
	if len(codes) != len(want) {
		t.Fatalf("codes=%q want=%q", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes=%q want=%q", codes, want)
		}
	}
}

func TestQRCodeDecoderDecodeRejectsMultiplePayloads(t *testing.T) {
	data := encodeQRCodeSheet(t, []string{"SUPPLIER-opaque-000001", "SUPPLIER-opaque-000002"})
	if _, err := (QRCodeDecoder{}).Decode(context.Background(), data); err == nil || !strings.Contains(err.Error(), "包含 2 个不同二维码") {
		t.Fatalf("expected ambiguous single-code error, got %v", err)
	}
}

func encodeQRCodeSheet(t *testing.T, payloads []string) []byte {
	t.Helper()
	const (
		qrSize = 240
		gap    = 24
	)
	img := image.NewGray(image.Rect(0, 0, len(payloads)*qrSize+(len(payloads)-1)*gap, qrSize))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	writer := qrcode.NewQRCodeWriter()
	for index, payload := range payloads {
		matrix, err := writer.Encode(payload, gozxing.BarcodeFormat_QR_CODE, qrSize, qrSize, nil)
		if err != nil {
			t.Fatal(err)
		}
		offsetX := index * (qrSize + gap)
		for y := 0; y < qrSize; y++ {
			for x := 0; x < qrSize; x++ {
				if matrix.Get(x, y) {
					img.SetGray(offsetX+x, y, color.Gray{Y: 0})
				}
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// Optional local acceptance against the user's existing supplier application.
// No private artifacts or decoded admission codes are copied into the repo/log.
func TestQRCodeDecoderLocalSupplierSample(t *testing.T) {
	path := os.Getenv("TICKET_ZYB_SAMPLE_LOG")
	if path == "" {
		t.Skip("optional local supplier sample")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		var row struct {
			Response struct {
				Artifact *struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"artifact"`
			} `json:"response"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Response.Artifact == nil || row.Response.Artifact.Type != "image" {
			continue
		}
		parts := strings.SplitN(row.Response.Artifact.Value, ",", 2)
		if len(parts) != 2 {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal("invalid sample image")
		}
		code, err := (QRCodeDecoder{}).Decode(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) == 0 || len(code) > 50 {
			t.Fatalf("supplier payload length incompatible: %d", len(code))
		}
		t.Logf("Existing supplier QR decoded; payload length=%d (payload withheld)", len(code))
		return
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("no image sample found")
}
