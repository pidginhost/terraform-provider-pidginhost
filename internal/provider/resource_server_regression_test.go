package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	pidginhost "github.com/pidginhost/sdk-go"
)

func serverTestPlan(t *testing.T, model serverModel) tfsdk.Plan {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	(&serverResource{}).Schema(t.Context(), resource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(t.Context(), model); diags.HasError() {
		t.Fatal(diags)
	}
	return plan
}

func serverTestModel() serverModel {
	m, _ := upgradeServerV0(serverModelV0{
		Hostname: types.StringValue("web1"), Image: types.StringValue("ubuntu-24"), Package: types.StringValue("cloudv-1"),
	})
	return m
}

func TestServerCreateSavesProtection(t *testing.T) {
	fake := newFakeServerAPI()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	oldInterval := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = oldInterval })
	r := &serverResource{client: newAPIClient("test", srv.URL, "test")}
	m := serverTestModel()
	m.DestroyProtection = types.BoolValue(true)
	plan := serverTestPlan(t, m)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema, Raw: tftypes.NewValue(plan.Raw.Type(), nil)}}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan, Config: tfsdk.Config(plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var protected types.Bool
	if diags := resp.State.GetAttribute(t.Context(), path.Root("destroy_protection"), &protected); diags.HasError() {
		t.Fatal(diags)
	}
	if !protected.ValueBool() {
		t.Fatal("create saved disabled protection after enabling it")
	}
}

func TestServerCreateKeepsIDWhenWaitFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":101}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"cannot read server"}`))
	}))
	defer srv.Close()
	r := &serverResource{client: newAPIClient("test", srv.URL, "test")}
	plan := serverTestPlan(t, serverTestModel())
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema, Raw: tftypes.NewValue(plan.Raw.Type(), nil)}}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan, Config: tfsdk.Config(plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected wait error")
	}
	var id types.String
	if diags := resp.State.GetAttribute(t.Context(), path.Root("id"), &id); diags.HasError() {
		t.Fatal(diags)
	}
	if id.ValueString() != "101" {
		t.Fatal("created server id was lost")
	}
}

func TestServerTerminalStatuses(t *testing.T) {
	for _, status := range []string{"failed", "cancelling", "cancelled", "terminating", "terminated"} {
		t.Run(status, func(t *testing.T) {
			fake := newFakeServerAPI()
			fake.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/cloud/servers/",
				strings.NewReader(`{"hostname":"web1","image":"ubuntu-24","package":"cloudv-1"}`)))
			fake.detail["status"] = status
			srv := httptest.NewServer(fake)
			defer srv.Close()
			r := &serverResource{client: newAPIClient("test", srv.URL, "test")}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if _, err := r.waitUntilActive(ctx, 101, nil); err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("want immediate terminal status error for %s, got %v", status, err)
			}
			if isGone(status) {
				m := serverTestModel()
				m.ID = types.StringValue("101")
				plan := serverTestPlan(t, m)
				resp := resource.DeleteResponse{}
				r.Delete(t.Context(), resource.DeleteRequest{State: tfsdk.State(plan)}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				fake.mu.Lock()
				defer fake.mu.Unlock()
				for _, req := range fake.requests {
					if req == "DELETE /api/cloud/servers/101/" {
						t.Fatal("deleted an already deleted server again")
					}
				}
			}
		})
	}
}

func TestServerRefreshClearsMissingFirewall(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	r := &serverResource{client: newAPIClient("test", srv.URL, "test")}
	m := serverTestModel()
	m.FwRulesSet = types.StringValue("web")
	m.FwPolicyIn = types.StringValue("DROP")
	m.FwPolicyOut = types.StringValue("ACCEPT")
	if diags := r.refresh(t.Context(), &m, &pidginhost.ServerDetail{Id: 101}); diags.HasError() {
		t.Fatal(diags)
	}
	if m.FwRulesSet.ValueString() != "" || !m.FwPolicyIn.IsNull() || !m.FwPolicyOut.IsNull() {
		t.Fatal("refresh retained firewall settings after the public interface disappeared")
	}
}

func protocolValue(t *testing.T, typ tftypes.Type, values map[string]any) *tfprotov6.DynamicValue {
	t.Helper()
	attrs := make(map[string]tftypes.Value)
	for name, attrType := range typ.(tftypes.Object).AttributeTypes {
		attrs[name] = tftypes.NewValue(attrType, values[name])
	}
	v, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, attrs))
	if err != nil {
		t.Fatal(err)
	}
	return &v
}

func checkProtocolDiagnostics(t *testing.T, diags []*tfprotov6.Diagnostic, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s", d.Summary, d.Detail)
		}
	}
}

func TestServerV0UpgradeAndPlan(t *testing.T) {
	ctx := t.Context()
	server := providerserver.NewProtocol6(New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	checkProtocolDiagnostics(t, schemas.Diagnostics, err)
	old := map[string]any{
		"id": "101", "hostname": "web1", "image": "ubuntu-24", "package": "cloudv-1", "status": "running",
		"firewall_rules_create": true, "firewall_rules_set_id": "7", "fw_rules_set": "web",
		"extra_volume_id": "9", "extra_volume_alias": "extra", "destroy_public_ipv4": true,
		"firewall_rules": []any{map[string]any{
			"direction": "in", "action": "ACCEPT", "protocol": "tcp", "source": "192.0.2.0/24", "sport": nil,
			"destination": nil, "dport": "22", "enabled": true, "position": "0",
		}},
	}
	for name := range serverSchemaV0().Attributes {
		if _, ok := old[name]; !ok {
			old[name] = nil
		}
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: "pidginhost_server", Version: 0, RawState: &tfprotov6.RawState{JSON: raw},
	})
	checkProtocolDiagnostics(t, upgraded.Diagnostics, err)
	if len(upgraded.Diagnostics) != 2 {
		t.Fatalf("want warnings for both retained objects, got %d", len(upgraded.Diagnostics))
	}
	typ := schemas.ResourceSchemas["pidginhost_server"].ValueType()
	value, err := upgraded.UpgradedState.Unmarshal(typ)
	if err != nil {
		t.Fatal(err)
	}
	var attrs map[string]tftypes.Value
	if err := value.As(&attrs); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"password_wo", "password_wo_version", "user_data_wo", "user_data_wo_version"} {
		if !attrs[name].IsNull() {
			t.Fatalf("upgraded %s must be null", name)
		}
	}
	var id string
	if err := attrs["id"].As(&id); err != nil || id != "101" {
		t.Fatal("upgrade lost the server id")
	}
	config := protocolValue(t, typ, map[string]any{
		"hostname": "web1", "image": "ubuntu-24", "package": "cloudv-1", "fw_rules_set": "web", "destroy_public_ipv4": true,
	})
	planned, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "pidginhost_server", PriorState: upgraded.UpgradedState, ProposedNewState: upgraded.UpgradedState, Config: config,
	})
	checkProtocolDiagnostics(t, planned.Diagnostics, err)
	if len(planned.RequiresReplace) != 0 {
		t.Fatalf("upgrading state requires replacement: %v", planned.RequiresReplace)
	}
}
