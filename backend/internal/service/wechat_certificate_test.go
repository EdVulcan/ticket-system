package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func makeWechatCertificate(t *testing.T, merchantID string, key *rsa.PrivateKey, now time.Time) []byte {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x1234ABCD),
		Subject:      pkix.Name{CommonName: merchantID},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestParseWechatMerchantCertificate(t *testing.T) {
	now := time.Now()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := makeWechatCertificate(t, "1602588787", key, now)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	got, err := ParseWechatMerchantCertificate(certPEM, keyPEM, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.MerchantID != "1602588787" || got.SerialNo != "1234ABCD" || got.PrivateKey == "" || got.Fingerprint == "" {
		t.Fatalf("unexpected certificate result: %+v", got)
	}
}

func TestParseWechatMerchantCertificateRejectsMismatchedPrivateKey(t *testing.T) {
	now := time.Now()
	certKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	certPEM := makeWechatCertificate(t, "1602588787", certKey, now)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(otherKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if _, err := ParseWechatMerchantCertificate(certPEM, keyPEM, now); err == nil {
		t.Fatal("mismatched merchant private key was accepted")
	}
}
