package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestLBFirewallPreservesOmittedAndEmptyRules(t *testing.T) {
	for _, omitted := range []bool{true, false} {
		t.Run(fmt.Sprint(omitted), func(t *testing.T) {
			client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
				return 200, pidginhost.PaginatedLBFirewallRuleList{Results: []pidginhost.LBFirewallRule{}}
			})
			r := &kubernetesLBFirewallResource{client: client}
			s := testSchema(r)
			model := kubernetesLBFirewallModel{ClusterID: types.StringValue("7")}
			if !omitted {
				model.Rules = []lbFirewallRuleModel{}
			}
			resp := resource.CreateResponse{State: testState(t, s, nil)}
			r.Create(t.Context(), resource.CreateRequest{Plan: testPlan(t, s, model)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var rules types.List
			if diags := resp.State.GetAttribute(t.Context(), path.Root("rule"), &rules); diags.HasError() {
				t.Fatal(diags)
			}
			if rules.IsNull() != omitted || len(rules.Elements()) != 0 {
				t.Fatalf("omitted=%v: rules=%v", omitted, rules)
			}
		})
	}
}

func TestLBFirewallCreateKeepsIdentityAfterPartialFailure(t *testing.T) {
	posts := 0
	client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		switch call {
		case "GET /api/kubernetes/clusters/7/lb-firewall/?page=1":
			return 200, pidginhost.PaginatedLBFirewallRuleList{Results: []pidginhost.LBFirewallRule{}}
		case "POST /api/kubernetes/clusters/7/lb-firewall/":
			posts++
			if posts == 1 {
				return 201, pidginhost.LBFirewallRule{Id: 1}
			}
			return 400, map[string]any{"detail": "rule refused"}
		}
		return 500, map[string]any{"detail": "unexpected call"}
	})
	r := &kubernetesLBFirewallResource{client: client}
	s := testSchema(r)
	model := kubernetesLBFirewallModel{ClusterID: types.StringValue("7")}
	model.setRules([]lbRule{lbWebRule(0), {Direction: "in", Action: "DROP", Enabled: true, Position: 200}})
	resp := resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(t.Context(), resource.CreateRequest{Plan: testPlan(t, s, model)}, &resp)
	if !resp.Diagnostics.HasError() || posts != 2 {
		t.Fatalf("wanted a partial create failure: posts=%d diagnostics=%v", posts, resp.Diagnostics)
	}
	for _, name := range []string{"id", "cluster_id"} {
		var value types.String
		if diags := resp.State.GetAttribute(t.Context(), path.Root(name), &value); diags.HasError() {
			t.Fatal(diags)
		}
		if value.ValueString() != "7" {
			t.Errorf("partial create lost %s", name)
		}
	}
}

func TestLBFirewallEmptyLifecycleAgainstFakeAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/kubernetes/clusters/7/lb-firewall/" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		k8sReply(w, http.StatusOK, `{"count":0,"next":null,"previous":null,"results":[]}`)
	}))
	defer srv.Close()
	config := func(rules string) string {
		return fmt.Sprintf(`
provider "pidginhost" {
  token = "test"
  base_url = %q
}
resource "pidginhost_kubernetes_lb_firewall" "test" {
  cluster_id = "7"
  %s
}
`, srv.URL, rules)
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{Config: config("")},
			{Config: config("rule = []")},
			{Config: config("")},
		},
	})
}

func TestPublicIPDeleteWaitsForDetach(t *testing.T) {
	for _, version := range []int64{4, 6} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ip := pidginhost.PublicIPv4{Id: 3, Address: "192.0.2.3", Attached: true,
				ServerId: *pidginhost.NewNullableInt32(pidginhost.PtrInt32(5))}
			polls := 0
			base := fmt.Sprintf("/api/cloud/ipv%d/3/", version)
			client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
				switch call {
				case "GET " + base:
					if polls > 0 {
						polls--
						if polls == 0 {
							ip.Attached = false
							ip.ServerId.Unset()
						}
					}
					return 200, ip
				case "POST " + base + "detach/":
					polls = 2
					return 200, map[string]any{"detached": true}
				case "DELETE " + base:
					if ipBound(&ip) {
						return 400, map[string]any{"detail": "address still attached"}
					}
					return 204, nil
				}
				return 500, nil
			})
			r := &publicIPResource{client: client}
			s := testSchema(r)
			state := testState(t, s, publicIPModel{ID: types.StringValue("3"), Version: types.Int64Value(version),
				Timeouts: nullTimeouts(t, s)})
			resp := resource.DeleteResponse{}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !slices.Contains(calls(), "DELETE "+base) {
				t.Fatal("address was not released")
			}
		})
	}
}

func TestPublicIPBindingRefusesUnknownOwner(t *testing.T) {
	for _, want := range []string{"", "7"} {
		if _, _, err := planIPBinding(true, "", "", want); err == nil {
			t.Errorf("accepted a binding with an unknown owner for target %q", want)
		}
	}
}

func TestPublicIPDeleteHonorsTimeout(t *testing.T) {
	client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		if call == "POST /api/cloud/ipv4/3/detach/" {
			return 200, map[string]any{"detached": true}
		}
		return 200, pidginhost.PublicIPv4{Id: 3, Attached: true}
	})
	r := &publicIPResource{client: client}
	s := testSchema(r)
	timeout := nullTimeouts(t, s)
	timeout.Object = types.ObjectValueMust(timeout.AttributeTypes(t.Context()), map[string]attr.Value{
		"update": types.StringNull(), "delete": types.StringValue("30ms"),
	})
	state := testState(t, s, publicIPModel{ID: types.StringValue("3"), Version: types.Int64Value(4), Timeouts: timeout})
	resp := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
		t.Fatal("a timed out detach must keep the address managed")
	}
	if slices.Contains(calls(), "DELETE /api/cloud/ipv4/3/") {
		t.Fatal("released the address while detachment was still pending")
	}
}

func TestSnapshotReadKeepsPendingDeletion(t *testing.T) {
	client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		return 200, []pidginhost.Snapshot{{Name: "backup", State: "delete"}}
	})
	r := &serverSnapshotResource{client: client}
	s := testSchema(r)
	state := testState(t, s, serverSnapshotModel{ID: types.StringValue("7/backup"),
		ServerID: types.StringValue("7"), Name: types.StringValue("backup"), Timeouts: nullTimeouts(t, s)})
	resp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("forgot a snapshot before deletion finished; replacement can race the deletion")
	}
}

func TestVolumeMoveWaitsForAttachmentOnTarget(t *testing.T) {
	vol := pidginhost.Volume{Id: 7, Size: 20, Product: "ssd"}
	volumeReads, serverReads := 0, 0
	client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/volumes/7/":
			if vol.Server != "" {
				volumeReads++
				vol.Attached = volumeReads >= 2
			}
			return 200, vol
		case "POST /api/cloud/volumes/7/attach/":
			vol.Server = "web"
			return 200, map[string]any{"attached": true}
		case "GET /api/cloud/servers/2/":
			serverReads++
			server := pidginhost.ServerDetail{Id: 2, Hostname: "web", Status: "active"}
			if serverReads >= 2 {
				server.Volumes = []pidginhost.Volume{vol}
			}
			return 200, server
		}
		return 500, map[string]any{"detail": "unexpected call"}
	})
	r := &volumeResource{client: client}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got, err := r.move(ctx, 7, "2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Attached || serverReads < 2 {
		t.Fatal("move completed before the target server had the attached volume")
	}
}

func TestGatewayAPIChangesRequireReplacement(t *testing.T) {
	r := &kubernetesClusterResource{}
	s := testSchema(r)
	for _, tc := range []struct {
		name          string
		before, after types.Bool
	}{
		{"disable default", types.BoolNull(), types.BoolValue(false)},
		{"restore default", types.BoolValue(false), types.BoolNull()},
		{"toggle", types.BoolValue(true), types.BoolValue(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := kubernetesClusterModel{ID: types.StringValue("7"), Features: types.SetNull(types.StringType),
				EnableGatewayAPI: tc.before, Timeouts: nullTimeouts(t, s)}
			state := testState(t, s, m)
			m.EnableGatewayAPI = tc.after
			plan := testPlan(t, s, m)
			resp := planmodifier.BoolResponse{PlanValue: tc.after}
			for _, modifier := range s.Attributes["enable_gateway_api"].(schema.BoolAttribute).PlanModifiers {
				modifier.PlanModifyBool(t.Context(), planmodifier.BoolRequest{
					State: state, Plan: plan, StateValue: tc.before, PlanValue: tc.after,
				}, &resp)
			}
			if resp.Diagnostics.HasError() || !resp.RequiresReplace {
				t.Fatalf("create-only setting changed without replacement: %v", resp.Diagnostics)
			}
		})
	}
}
