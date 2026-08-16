package service

import (
	"encoding/json"
	"testing"
)

func TestAgentQueryResultJSONIsRecognizedAndStoredAsASet(t *testing.T) {
	query := `{"schema_version":"1","module":"orders","tool":"search_orders","as_of":"2026-08-17T00:00:00Z","data":[],"returned":0,"total":0,"has_more":false}`
	if !isAgentQueryResultJSON(query) {
		t.Fatal("valid server-owned QueryResult was not recognized")
	}
	if isAgentQueryResultJSON(`{"module":"orders","tool":"search_orders"}`) {
		t.Fatal("unversioned query result was accepted")
	}

	payload, err := json.Marshal(agentQueryResultSet{QueryResults: []json.RawMessage{json.RawMessage(query)}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		QueryResults []json.RawMessage `json:"query_results"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.QueryResults) != 1 || string(decoded.QueryResults[0]) != query {
		t.Fatalf("stored QueryResult set=%s", payload)
	}
}
