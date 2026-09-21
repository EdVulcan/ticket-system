package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestXiaohongshuPhoneAuthAdapterVerifiesPlatformPayload(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	key := []byte("xiaohongshu-key!")
	payload := map[string]any{
		"phoneNumber":     "+86 13800138000",
		"purePhoneNumber": "13800138000",
		"countryCode":     "86",
		"watermark": map[string]any{
			"appid":     "wx-or-xhs-test-app",
			"timestamp": now.Add(-30 * time.Second).Unix(),
		},
	}
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, key, payload)
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return now },
	}
	result, err := adapter.Verify(XiaohongshuPhoneAuthRequest{
		AppID:                "wx-or-xhs-test-app",
		SessionKeyCiphertext: "stored-session-key-ciphertext",
		EncryptedData:        encryptedData,
		IV:                   iv,
	})
	if err != nil {
		t.Fatalf("verify phone: %v", err)
	}
	if result.PhoneNumber != "+86 13800138000" || result.PurePhoneNumber != "13800138000" || result.CountryCode != "86" || result.WatermarkAppID != "wx-or-xhs-test-app" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestXiaohongshuPhoneAuthAdapterRejectsAppIDMismatch(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	key := []byte("xiaohongshu-key!")
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, key, validXiaohongshuPhonePayload(now, "other-app"))
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return now },
	}
	_, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "expected-app", SessionKeyCiphertext: "stored", EncryptedData: encryptedData, IV: iv})
	if !errors.Is(err, ErrXiaohongshuPhoneAuthAppID) {
		t.Fatalf("err=%v, want appid mismatch", err)
	}
}

func TestXiaohongshuPhoneAuthAdapterRejectsWrongSessionKey(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	realKey := []byte("xiaohongshu-key!")
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, realKey, validXiaohongshuPhonePayload(now, "test-app"))
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return "wrong-session-key!", nil },
		Now:               func() time.Time { return now },
	}
	_, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "test-app", SessionKeyCiphertext: "stored", EncryptedData: encryptedData, IV: iv})
	if !errors.Is(err, ErrXiaohongshuPhoneAuthSessionKey) {
		t.Fatalf("err=%v, want session key error", err)
	}
}

func TestXiaohongshuPhoneAuthAdapterRejectsMalformedPayload(t *testing.T) {
	key := []byte("xiaohongshu-key!")
	_, iv := encryptXiaohongshuPhoneFixture(t, key, map[string]any{"invalid": true})
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC) },
	}
	_, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "test-app", SessionKeyCiphertext: "stored", EncryptedData: "not-base64", IV: iv})
	if !errors.Is(err, ErrXiaohongshuPhoneAuthPayload) {
		t.Fatalf("err=%v, want malformed payload", err)
	}
}

func TestXiaohongshuPhoneAuthAdapterRejectsExpiredWatermark(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	key := []byte("xiaohongshu-key!")
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, key, validXiaohongshuPhonePayload(now.Add(-11*time.Minute), "test-app"))
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return now },
	}
	_, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "test-app", SessionKeyCiphertext: "stored", EncryptedData: encryptedData, IV: iv})
	if !errors.Is(err, ErrXiaohongshuPhoneAuthExpired) {
		t.Fatalf("err=%v, want expired watermark", err)
	}
}

func TestXiaohongshuPhoneAuthAdapterAcceptsCamelCaseWatermarkAppID(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	key := []byte("xiaohongshu-key!")
	payload := validXiaohongshuPhonePayload(now, "test-app")
	watermark := payload["watermark"].(map[string]any)
	delete(watermark, "appid")
	watermark["appId"] = "test-app"
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, key, payload)
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return now },
	}
	result, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "test-app", SessionKeyCiphertext: "stored", EncryptedData: encryptedData, IV: iv})
	if err != nil {
		t.Fatalf("verify camel-case watermark appId: %v", err)
	}
	if result.WatermarkAppID != "test-app" {
		t.Fatalf("watermark app id=%q, want test-app", result.WatermarkAppID)
	}
}

func TestXiaohongshuPhoneAuthAdapterRejectsConflictingWatermarkAppIDs(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	key := []byte("xiaohongshu-key!")
	payload := validXiaohongshuPhonePayload(now, "test-app")
	payload["watermark"].(map[string]any)["appId"] = "other-app"
	encryptedData, iv := encryptXiaohongshuPhoneFixture(t, key, payload)
	adapter := &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: func(string) (string, error) { return string(key), nil },
		Now:               func() time.Time { return now },
	}
	_, err := adapter.Verify(XiaohongshuPhoneAuthRequest{AppID: "test-app", SessionKeyCiphertext: "stored", EncryptedData: encryptedData, IV: iv})
	if !errors.Is(err, ErrXiaohongshuPhoneAuthAppID) {
		t.Fatalf("err=%v, want conflicting appid rejection", err)
	}
}

func validXiaohongshuPhonePayload(at time.Time, appID string) map[string]any {
	return map[string]any{
		"phoneNumber":     "+86 13800138000",
		"purePhoneNumber": "13800138000",
		"countryCode":     "86",
		"watermark": map[string]any{
			"appid":     appID,
			"timestamp": at.Unix(),
		},
	}
}

func encryptXiaohongshuPhoneFixture(t *testing.T, key []byte, payload map[string]any) (string, string) {
	t.Helper()
	plaintext, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	plaintext = append(plaintext, bytesRepeat(byte(padding), padding)...)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(iv)
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}
