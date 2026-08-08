package api

import (
	"encoding/json"
	"testing"
)

func TestTenantRequestsIgnoreLifecycleFields(t *testing.T) {
	createPayload := []byte(`{
		"name":"山河旅行社",
		"system_code":"SHLX001",
		"admin_password":"Travel-Password-123",
		"qualification_expires_at":"",
		"contract_expires_at":"",
		"qualification_status":"approved"
	}`)
	var create CreateTenantRequest
	if err := json.Unmarshal(createPayload, &create); err != nil {
		t.Fatalf("create request rejected legacy lifecycle fields: %v", err)
	}
	if create.Name != "山河旅行社" || create.SystemCode != "SHLX001" {
		t.Fatalf("unexpected create request: %+v", create)
	}

	updatePayload := []byte(`{
		"name":"山河旅行社",
		"qualification_expires_at":"",
		"contract_expires_at":""
	}`)
	var update UpdateTenantRequest
	if err := json.Unmarshal(updatePayload, &update); err != nil {
		t.Fatalf("update request rejected legacy lifecycle fields: %v", err)
	}
	if update.Name != "山河旅行社" {
		t.Fatalf("unexpected update request: %+v", update)
	}
}
