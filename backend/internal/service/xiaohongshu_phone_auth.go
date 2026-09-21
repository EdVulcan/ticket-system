package service

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"ticket-backend/internal/utils"
	"time"
)

const (
	// XiaohongshuPhoneAuthDefaultWatermarkAge is intentionally short. The
	// encrypted payload is a one-time authorization assertion and should not be
	// accepted long after the user pressed the platform authorization button.
	XiaohongshuPhoneAuthDefaultWatermarkAge = 10 * time.Minute
	XiaohongshuPhoneAuthDefaultFutureSkew   = 2 * time.Minute
)

var (
	ErrXiaohongshuPhoneAuthInvalid      = errors.New("xiaohongshu phone authorization is invalid")
	ErrXiaohongshuPhoneAuthSessionKey   = errors.New("xiaohongshu phone authorization session key is invalid")
	ErrXiaohongshuPhoneAuthPayload      = errors.New("xiaohongshu phone authorization payload is invalid")
	ErrXiaohongshuPhoneAuthAppID        = errors.New("xiaohongshu phone authorization appid does not match")
	ErrXiaohongshuPhoneAuthExpired      = errors.New("xiaohongshu phone authorization has expired")
	ErrXiaohongshuPhoneAuthPhoneMissing = errors.New("xiaohongshu phone authorization phone is missing")
)

// XiaohongshuPhoneAuthRequest contains the server-side channel identity and
// the encrypted fields returned by the mini program. SessionKeyCiphertext is
// the value persisted in MiniappCustomer; callers must load it from the
// authenticated customer instead of accepting a client-supplied session key.
type XiaohongshuPhoneAuthRequest struct {
	AppID                string
	SessionKeyCiphertext string
	EncryptedData        string
	IV                   string
}

// XiaohongshuPhoneVerificationResult is platform evidence. It does not create
// or update a member; the caller must pass the verified phone to MemberService
// together with the authenticated tenant/member scope.
type XiaohongshuPhoneVerificationResult struct {
	PhoneNumber     string
	PurePhoneNumber string
	CountryCode     string
	WatermarkAppID  string
	WatermarkAt     time.Time
}

// XiaohongshuPhoneAuthAdapter decrypts and validates the platform assertion.
// DecryptSessionKey is injectable for tests and for deployments that use a
// key-management wrapper around the repository's encrypted-at-rest helper.
type XiaohongshuPhoneAuthAdapter struct {
	DecryptSessionKey func(string) (string, error)
	Now               func() time.Time
	MaxWatermarkAge   time.Duration
	FutureSkew        time.Duration
}

// NewXiaohongshuPhoneAuthAdapter constructs the production adapter. It does
// not retain any session key or phone data between calls.
func NewXiaohongshuPhoneAuthAdapter() *XiaohongshuPhoneAuthAdapter {
	return &XiaohongshuPhoneAuthAdapter{
		DecryptSessionKey: utils.DecryptAES,
		Now:               time.Now,
		MaxWatermarkAge:   XiaohongshuPhoneAuthDefaultWatermarkAge,
		FutureSkew:        XiaohongshuPhoneAuthDefaultFutureSkew,
	}
}

type xiaohongshuPhonePayload struct {
	PhoneNumber     string                    `json:"phoneNumber"`
	PurePhoneNumber string                    `json:"purePhoneNumber"`
	CountryCode     string                    `json:"countryCode"`
	Watermark       xiaohongshuPhoneWatermark `json:"watermark"`
}

// Xiaohongshu payloads in the wild use both the documented camel-case appId
// and the legacy lower-case appid spelling. Keep both during decoding so the
// server can accept either spelling without weakening the AppID check.
type xiaohongshuPhoneWatermark struct {
	AppID       string `json:"appid"`
	CanonicalID string `json:"appId"`
	Timestamp   int64  `json:"timestamp"`
}

func (w xiaohongshuPhoneWatermark) normalizedAppID() (string, bool) {
	legacy := strings.TrimSpace(w.AppID)
	canonical := strings.TrimSpace(w.CanonicalID)
	if legacy != "" && canonical != "" && legacy != canonical {
		return "", false
	}
	if canonical != "" {
		return canonical, true
	}
	return legacy, legacy != ""
}

// Verify decrypts the encrypted phone payload using the server-side session
// key and validates the app identity, phone fields, and timestamp. It never
// returns an error containing the encrypted payload or session key.
func (a *XiaohongshuPhoneAuthAdapter) Verify(request XiaohongshuPhoneAuthRequest) (XiaohongshuPhoneVerificationResult, error) {
	if a == nil {
		return XiaohongshuPhoneVerificationResult{}, ErrXiaohongshuPhoneAuthInvalid
	}
	appID := strings.TrimSpace(request.AppID)
	if appID == "" || strings.TrimSpace(request.SessionKeyCiphertext) == "" || strings.TrimSpace(request.EncryptedData) == "" || strings.TrimSpace(request.IV) == "" {
		return XiaohongshuPhoneVerificationResult{}, ErrXiaohongshuPhoneAuthInvalid
	}
	decrypt := a.DecryptSessionKey
	if decrypt == nil {
		decrypt = utils.DecryptAES
	}
	sessionKey, err := decrypt(strings.TrimSpace(request.SessionKeyCiphertext))
	if err != nil || strings.TrimSpace(sessionKey) == "" {
		return XiaohongshuPhoneVerificationResult{}, ErrXiaohongshuPhoneAuthSessionKey
	}
	plaintext, err := decryptXiaohongshuPhonePayload(sessionKey, request.EncryptedData, request.IV)
	if err != nil {
		if errors.Is(err, ErrXiaohongshuPhoneAuthSessionKey) {
			return XiaohongshuPhoneVerificationResult{}, ErrXiaohongshuPhoneAuthSessionKey
		}
		return XiaohongshuPhoneVerificationResult{}, fmt.Errorf("%w: %v", ErrXiaohongshuPhoneAuthPayload, err)
	}
	var payload xiaohongshuPhonePayload
	decoder := json.NewDecoder(strings.NewReader(plaintext))
	if err := decoder.Decode(&payload); err != nil {
		return XiaohongshuPhoneVerificationResult{}, fmt.Errorf("%w: invalid json", ErrXiaohongshuPhoneAuthPayload)
	}
	if err := validateXiaohongshuPhonePayload(appID, payload, a.now(), a.maxAge(), a.futureSkew()); err != nil {
		return XiaohongshuPhoneVerificationResult{}, err
	}
	watermarkAppID, _ := payload.Watermark.normalizedAppID()
	return XiaohongshuPhoneVerificationResult{
		PhoneNumber:     strings.TrimSpace(payload.PhoneNumber),
		PurePhoneNumber: payload.PurePhoneNumber,
		CountryCode:     payload.CountryCode,
		WatermarkAppID:  watermarkAppID,
		WatermarkAt:     time.Unix(payload.Watermark.Timestamp, 0).UTC(),
	}, nil
}

func (a *XiaohongshuPhoneAuthAdapter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *XiaohongshuPhoneAuthAdapter) maxAge() time.Duration {
	if a.MaxWatermarkAge > 0 {
		return a.MaxWatermarkAge
	}
	return XiaohongshuPhoneAuthDefaultWatermarkAge
}

func (a *XiaohongshuPhoneAuthAdapter) futureSkew() time.Duration {
	if a.FutureSkew > 0 {
		return a.FutureSkew
	}
	return XiaohongshuPhoneAuthDefaultFutureSkew
}

func decryptXiaohongshuPhonePayload(sessionKey, encryptedData, iv string) (string, error) {
	encodedKey := strings.TrimSpace(sessionKey)
	if encodedKey == "" {
		return "", ErrXiaohongshuPhoneAuthSessionKey
	}
	keys := [][]byte{[]byte(encodedKey)}
	// The platform's code2session response uses a Base64-encoded AES key,
	// while older fixtures and compatible environments may expose the raw key.
	// Try the raw representation first for backward compatibility, then the
	// decoded bytes without logging or persisting either representation.
	if decoded, err := base64.StdEncoding.DecodeString(encodedKey); err == nil && len(decoded) > 0 {
		keys = append(keys, decoded)
	}
	ivBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(iv))
	if err != nil || len(ivBytes) != aes.BlockSize {
		return "", ErrXiaohongshuPhoneAuthPayload
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptedData))
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", ErrXiaohongshuPhoneAuthPayload
	}
	var lastErr error
	for _, key := range keys {
		plaintext, decryptErr := decryptXiaohongshuPhoneCiphertext(key, ivBytes, ciphertext)
		if decryptErr == nil {
			return plaintext, nil
		}
		lastErr = decryptErr
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrXiaohongshuPhoneAuthSessionKey
}

func decryptXiaohongshuPhoneCiphertext(key, ivBytes, ciphertext []byte) (string, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return "", ErrXiaohongshuPhoneAuthSessionKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrXiaohongshuPhoneAuthSessionKey
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, ivBytes).CryptBlocks(plaintext, ciphertext)
	plaintext, err = unpadXiaohongshuPKCS7(plaintext, aes.BlockSize)
	if err != nil {
		return "", ErrXiaohongshuPhoneAuthPayload
	}
	return string(plaintext), nil
}

func unpadXiaohongshuPKCS7(plaintext []byte, blockSize int) ([]byte, error) {
	if len(plaintext) == 0 || blockSize <= 0 || len(plaintext)%blockSize != 0 {
		return nil, ErrXiaohongshuPhoneAuthPayload
	}
	padding := int(plaintext[len(plaintext)-1])
	if padding == 0 || padding > blockSize || padding > len(plaintext) {
		return nil, ErrXiaohongshuPhoneAuthPayload
	}
	for _, b := range plaintext[len(plaintext)-padding:] {
		if int(b) != padding {
			return nil, ErrXiaohongshuPhoneAuthPayload
		}
	}
	return plaintext[:len(plaintext)-padding], nil
}

var xiaohongshuPhoneDigits = regexp.MustCompile(`^[0-9]{4,32}$`)
var xiaohongshuCountryCodeDigits = regexp.MustCompile(`^[0-9]{1,6}$`)

func validateXiaohongshuPhonePayload(appID string, payload xiaohongshuPhonePayload, now time.Time, maxAge, futureSkew time.Duration) error {
	watermarkAppID, matches := payload.Watermark.normalizedAppID()
	if !matches || watermarkAppID == "" || watermarkAppID != appID {
		return ErrXiaohongshuPhoneAuthAppID
	}
	if payload.Watermark.Timestamp <= 0 {
		return ErrXiaohongshuPhoneAuthExpired
	}
	watermarkAt := time.Unix(payload.Watermark.Timestamp, 0)
	if now.IsZero() {
		now = time.Now()
	}
	if watermarkAt.After(now.Add(futureSkew)) || now.Sub(watermarkAt) > maxAge {
		return ErrXiaohongshuPhoneAuthExpired
	}
	phone := strings.TrimSpace(payload.PhoneNumber)
	pure := strings.TrimSpace(payload.PurePhoneNumber)
	country := strings.TrimSpace(payload.CountryCode)
	if phone == "" || pure == "" || country == "" {
		return ErrXiaohongshuPhoneAuthPhoneMissing
	}
	if !xiaohongshuPhoneDigits.MatchString(pure) || !xiaohongshuCountryCodeDigits.MatchString(country) {
		return ErrXiaohongshuPhoneAuthPhoneMissing
	}
	digits := make([]byte, 0, len(phone))
	for i := 0; i < len(phone); i++ {
		if phone[i] >= '0' && phone[i] <= '9' {
			digits = append(digits, phone[i])
		}
	}
	if len(digits) == 0 || !strings.HasSuffix(string(digits), pure) {
		return ErrXiaohongshuPhoneAuthPhoneMissing
	}
	return nil
}
