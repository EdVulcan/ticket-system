package deviceprovision

import (
	"strings"
	"testing"
)

func TestBundleRoundTripAndTamperDetection(t *testing.T) {
	privateKey, publicKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	want := Bundle{ServerURL: "https://tickets.example", SystemCode: "SYS001", SerialNumber: "GATE-1", DeviceKey: "device-secret", MaintenanceSecret: "maintenance-secret"}
	envelope, err := EncryptBundle(want, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptBundle(envelope, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip bundle=%+v, want %+v", got, want)
	}
	if _, err := DecryptBundle(strings.Replace(envelope, "ciphertext", "ciphertextX", 1), privateKey); err == nil {
		t.Fatal("tampered envelope unexpectedly decrypted")
	}
	otherPrivate, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptBundle(envelope, otherPrivate); err == nil {
		t.Fatal("envelope decrypted with wrong private key")
	}
}

func TestPublicKeyEncodingIsCanonical(t *testing.T) {
	privateKey, publicKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicKey(EncodePublicKey(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(decoded) != Fingerprint(privateKey.PublicKey().Bytes()) {
		t.Fatal("public key fingerprint changed during encoding")
	}
}
