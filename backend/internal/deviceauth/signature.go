package deviceauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	HeaderSystemCode = "X-Device-System-Code"
	HeaderSerial     = "X-Device-Serial"
	HeaderRequestID  = "X-Device-Request-Id"
	HeaderTimestamp  = "X-Device-Timestamp"
	HeaderNonce      = "X-Device-Nonce"
	HeaderSignature  = "X-Device-Signature"
)

func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func DeriveKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func DecodeStoredKey(value string) ([]byte, error) {
	key, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != sha256.Size {
		return nil, errors.New("invalid stored device key")
	}
	return key, nil
}

func Canonical(method, path, timestamp, nonce, requestID, bodyHash string) string {
	return strings.Join([]string{strings.ToUpper(method), path, timestamp, nonce, requestID, bodyHash}, "\n")
}

func Sign(key []byte, method, path, timestamp, nonce, requestID string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(Canonical(method, path, timestamp, nonce, requestID, HashBody(body))))
	return hex.EncodeToString(mac.Sum(nil))
}

func Verify(key []byte, provided, method, path, timestamp, nonce, requestID string, body []byte) bool {
	expected := Sign(key, method, path, timestamp, nonce, requestID, body)
	provided = strings.ToLower(strings.TrimSpace(provided))
	return len(expected) == len(provided) && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
