package zyb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"

	"github.com/makiuchi-d/gozxing"
	multiqrcode "github.com/makiuchi-d/gozxing/multi/qrcode"
	"github.com/makiuchi-d/gozxing/qrcode"
)

const maxQRCodeImageSide = 4096

type QRCodeDecoder struct{}

func (QRCodeDecoder) Decode(ctx context.Context, data []byte) (string, error) {
	codes, err := (QRCodeDecoder{}).DecodeAll(ctx, data)
	if err != nil {
		return "", err
	}
	if len(codes) != 1 {
		return "", fmt.Errorf("识别二维码失败: 图片包含 %d 个不同二维码", len(codes))
	}
	return codes[0], nil
}

// DecodeAll returns every distinct, non-empty QR payload found in the image.
// Payloads are kept as decoded; ordering is normalized for deterministic callers.
func (QRCodeDecoder) DecodeAll(ctx context.Context, data []byte) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("二维码图片为空")
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("二维码图片过大")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析二维码图片尺寸失败: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxQRCodeImageSide || cfg.Height > maxQRCodeImageSide {
		return nil, errors.New("二维码图片尺寸不安全")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析二维码图片失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, fmt.Errorf("创建二维码图像失败: %w", err)
	}
	results, err := multiqrcode.NewQRCodeMultiReader().DecodeMultiple(bmp, nil)
	if err != nil {
		// Keep the single-code pure-barcode fallback for tightly cropped supplier
		// images that do not expose finder patterns to the multi-code detector.
		result, singleErr := qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_PURE_BARCODE: true})
		if singleErr != nil {
			return nil, fmt.Errorf("识别二维码失败: %w", err)
		}
		results = []*gozxing.Result{result}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(results))
	codes := make([]string, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		value := result.GetText()
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		codes = append(codes, value)
	}
	if len(codes) == 0 {
		return nil, errors.New("二维码内容为空")
	}
	sort.Strings(codes)
	return codes, nil
}
