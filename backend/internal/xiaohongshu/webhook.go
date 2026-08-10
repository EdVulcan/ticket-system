package xiaohongshu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

func MessageSignature(parts ...string) string {
	values := append([]string(nil), parts...)
	sort.Strings(values)
	digest := sha1.Sum([]byte(strings.Join(values, "")))
	return hex.EncodeToString(digest[:])
}

func VerifyMessageSignature(provided string, parts ...string) bool {
	expected := MessageSignature(parts...)
	provided = strings.ToLower(strings.TrimSpace(provided))
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func DecryptMessage(encrypted, encodingAESKey string) ([]byte, string, error) {
	encodingAESKey = strings.TrimSpace(encodingAESKey)
	if len(encodingAESKey) != 43 {
		return nil, "", errors.New("xiaohongshu EncodingAESKey must be 43 characters")
	}
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(key) != 32 {
		return nil, "", errors.New("xiaohongshu EncodingAESKey is invalid")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, "", errors.New("xiaohongshu encrypted message is invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plaintext, ciphertext)
	plaintext, err = removePKCS7Padding(plaintext)
	if err != nil {
		return nil, "", err
	}
	if len(plaintext) < 20 {
		return nil, "", errors.New("xiaohongshu decrypted message is too short")
	}
	messageLength := int(binary.BigEndian.Uint32(plaintext[16:20]))
	if messageLength < 0 || 20+messageLength > len(plaintext) {
		return nil, "", errors.New("xiaohongshu decrypted message length is invalid")
	}
	message := append([]byte(nil), plaintext[20:20+messageLength]...)
	appID := string(plaintext[20+messageLength:])
	if strings.TrimSpace(appID) == "" {
		return nil, "", errors.New("xiaohongshu decrypted message has no appid")
	}
	return message, appID, nil
}

func removePKCS7Padding(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("xiaohongshu decrypted message is empty")
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > 32 || padding > len(value) {
		return nil, errors.New("xiaohongshu message padding is invalid")
	}
	for _, item := range value[len(value)-padding:] {
		if int(item) != padding {
			return nil, errors.New("xiaohongshu message padding is invalid")
		}
	}
	return value[:len(value)-padding], nil
}
