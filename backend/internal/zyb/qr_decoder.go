package zyb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

const maxQRCodeImageSide = 4096

type QRCodeDecoder struct{}

func (QRCodeDecoder) Decode(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("二维码图片为空")
	}
	if len(data) > maxResponseBytes {
		return "", errors.New("二维码图片过大")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("解析二维码图片尺寸失败: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxQRCodeImageSide || cfg.Height > maxQRCodeImageSide {
		return "", errors.New("二维码图片尺寸不安全")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("解析二维码图片失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("创建二维码图像失败: %w", err)
	}
	result, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		// Supplier image endpoints also return tightly cropped, perfectly
		// aligned codes. Decode that form without the camera finder heuristic.
		result, err = qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_PURE_BARCODE: true})
	}
	if err != nil {
		return "", fmt.Errorf("识别二维码失败: %w", err)
	}
	value := result.GetText()
	if strings.TrimSpace(value) == "" {
		return "", errors.New("二维码内容为空")
	}
	return value, nil
}
