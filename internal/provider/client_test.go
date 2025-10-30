package provider

import (
	"encoding/json"
	"testing"
)

func TestServerIsReady(t *testing.T) {
	server := &Server{ID: json.Number("1"), Status: "active"}
	server.Machine.Status = "running"

	if !server.IsReady() {
		t.Fatalf("expected server to be ready")
	}

	server.Status = "provisioning"
	if server.IsReady() {
		t.Fatalf("expected server not ready when status != active")
	}
}

func TestMarshalCatalogToJSON(t *testing.T) {
	data := []map[string]any{{"id": 1, "name": "item"}}

	result, diags := marshalCatalogToJSON("images_json", data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	expected := `[{"id":1,"name":"item"}]`
	if result != expected {
		t.Fatalf("unexpected json: %s", result)
	}
}

func TestMarshalObjectToJSON(t *testing.T) {
	data := map[string]any{"id": 1, "name": "profile"}

	result, diags := marshalObjectToJSON("profile_json", data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	expected := `{"id":1,"name":"profile"}`
	if result != expected {
		t.Fatalf("unexpected json: %s", result)
	}
}
