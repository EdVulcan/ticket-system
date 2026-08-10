package xiaohongshu

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

func TestMessageSignatureMatchesOfficialOrdering(t *testing.T) {
	got := MessageSignature("token123", "1700000000", "nonce456", "encrypted789")
	if got != "75ae7ac08ccc81bce8d88113697d161cb1fe3f37" {
		t.Fatalf("signature = %s", got)
	}
	if !VerifyMessageSignature(got, "token123", "1700000000", "nonce456", "encrypted789") {
		t.Fatal("valid signature was rejected")
	}
}

func TestDecryptMessageValidatesPayloadAndAppID(t *testing.T) {
	key := "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789"
	payload := []byte(`{"Event":"PRODUCT_AUDIT","OutProductId":"P1"}`)
	encrypted := encryptWebhookFixture(t, key, payload, "sandbox-app")

	gotPayload, gotAppID, err := DecryptMessage(encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPayload) != string(payload) || gotAppID != "sandbox-app" {
		t.Fatalf("payload = %q, appid = %q", gotPayload, gotAppID)
	}
}

func encryptWebhookFixture(t *testing.T, encodingAESKey string, payload []byte, appID string) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := make([]byte, 20+len(payload)+len(appID))
	copy(plaintext[:16], []byte("0123456789abcdef"))
	binary.BigEndian.PutUint32(plaintext[16:20], uint32(len(payload)))
	copy(plaintext[20:], payload)
	copy(plaintext[20+len(payload):], appID)
	padding := 32 - len(plaintext)%32
	for range padding {
		plaintext = append(plaintext, byte(padding))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(ciphertext)
}
