// Package deviceprovision contains the small cryptographic envelope used by
// the one-time gate installation flow. It deliberately has no database or
// HTTP dependencies so the server and the Linux installer share exactly the
// same wire format.
package deviceprovision

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const EnvelopeVersion = 1

var curve = ecdh.X25519()

// Bundle contains only the values the installer needs to create its local
// environment file. Tenant and device ownership are intentionally absent:
// they are selected by the server from the activation lease.
type Bundle struct {
	ServerURL         string `json:"server_url"`
	SystemCode        string `json:"system_code"`
	SerialNumber      string `json:"serial_number"`
	DeviceKey         string `json:"device_key"`
	MaintenanceSecret string `json:"maintenance_secret"`
	MaintenanceURL    string `json:"maintenance_url,omitempty"`
}

// Envelope is safe to persist temporarily on the server. It can only be
// opened by the installer private key corresponding to InstallerPublicKey.
type Envelope struct {
	Version         int    `json:"version"`
	ServerPublicKey string `json:"server_public_key"`
	Nonce           string `json:"nonce"`
	Ciphertext      string `json:"ciphertext"`
}

func GenerateKeyPair() (*ecdh.PrivateKey, []byte, error) {
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, append([]byte(nil), privateKey.PublicKey().Bytes()...), nil
}

func EncodePublicKey(publicKey []byte) string {
	return base64.RawURLEncoding.EncodeToString(publicKey)
}

func DecodePublicKey(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("invalid X25519 public key")
	}
	return decoded, nil
}

func Fingerprint(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func EncryptBundle(bundle Bundle, installerPublicKey []byte) (string, error) {
	if len(installerPublicKey) != 32 {
		return "", errors.New("installer public key is required")
	}
	publicKey, err := curve.NewPublicKey(installerPublicKey)
	if err != nil {
		return "", fmt.Errorf("parse installer public key: %w", err)
	}
	serverPrivateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	shared, err := serverPrivateKey.ECDH(publicKey)
	if err != nil {
		return "", fmt.Errorf("derive provisioning key: %w", err)
	}
	key, err := deriveKey(shared)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte("ticket-gate-provision-v1"))
	envelope := Envelope{
		Version:         EnvelopeVersion,
		ServerPublicKey: EncodePublicKey(serverPrivateKey.PublicKey().Bytes()),
		Nonce:           base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:      base64.RawURLEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func DecryptBundle(encoded string, installerPrivateKey *ecdh.PrivateKey) (Bundle, error) {
	if installerPrivateKey == nil {
		return Bundle{}, errors.New("installer private key is required")
	}
	var envelope Envelope
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		return Bundle{}, fmt.Errorf("decode provisioning envelope: %w", err)
	}
	if envelope.Version != EnvelopeVersion {
		return Bundle{}, fmt.Errorf("unsupported provisioning envelope version %d", envelope.Version)
	}
	serverPublicBytes, err := DecodePublicKey(envelope.ServerPublicKey)
	if err != nil {
		return Bundle{}, err
	}
	serverPublicKey, err := curve.NewPublicKey(serverPublicBytes)
	if err != nil {
		return Bundle{}, err
	}
	shared, err := installerPrivateKey.ECDH(serverPublicKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("derive provisioning key: %w", err)
	}
	key, err := deriveKey(shared)
	if err != nil {
		return Bundle{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Bundle{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Bundle{}, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return Bundle{}, errors.New("invalid provisioning nonce")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return Bundle{}, errors.New("invalid provisioning ciphertext")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("ticket-gate-provision-v1"))
	if err != nil {
		return Bundle{}, errors.New("provisioning envelope authentication failed")
	}
	var bundle Bundle
	if err := json.Unmarshal(plaintext, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode provisioning bundle: %w", err)
	}
	if bundle.ServerURL == "" || bundle.SystemCode == "" || bundle.SerialNumber == "" || bundle.DeviceKey == "" {
		return Bundle{}, errors.New("provisioning bundle is incomplete")
	}
	return bundle, nil
}

func deriveKey(shared []byte) ([]byte, error) {
	reader := hkdf.New(sha256.New, shared, nil, []byte("ticket-gate-provision-v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}
