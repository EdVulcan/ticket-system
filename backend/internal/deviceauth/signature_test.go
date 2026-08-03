package deviceauth

import "testing"

func TestSignatureCoversRequestIdentityAndBody(t *testing.T) {
	key := DeriveKey("secret")
	body := []byte(`{"ticket_code":"T1"}`)
	signature := Sign(key, "POST", "/api/v1/hardware/verify", "100", "n1", "r1", body)
	if !Verify(key, signature, "POST", "/api/v1/hardware/verify", "100", "n1", "r1", body) {
		t.Fatal("signature should verify")
	}
	if Verify(key, signature, "POST", "/api/v1/hardware/verify", "100", "n1", "r2", body) {
		t.Fatal("changed request id must be rejected")
	}
	if Verify(key, signature, "POST", "/api/v1/hardware/verify", "100", "n1", "r1", []byte(`{"ticket_code":"T2"}`)) {
		t.Fatal("changed body must be rejected")
	}
}
