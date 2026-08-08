package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestGetProviderType(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{name: "wechat payment code", code: "132778209408855064", want: "wechat"},
		{name: "alipay 18 digit payment code", code: "282188738170985848", want: "alipay"},
		{name: "alipay minimum length", code: "2500000000000000", want: "alipay"},
		{name: "alipay maximum prefix", code: "309999999999999999", want: "alipay"},
		{name: "alipay code too short", code: "259999999999999", want: ""},
		{name: "alipay code too long", code: "2599999999999999999999999", want: ""},
		{name: "unsupported numeric prefix", code: "241234567890123456", want: ""},
		{name: "non numeric input", code: "28A188738170985848", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getProviderType(tt.code); got != tt.want {
				t.Fatalf("getProviderType(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestNewAlipayClientUsesProductionGateway(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	client, err := newAlipayProductionClient("2026000000000000", string(privateKeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	if !client.IsProd {
		t.Fatal("Alipay client must use the production gateway")
	}
}
