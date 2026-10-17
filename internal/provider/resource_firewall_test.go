package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccFirewall(t *testing.T) {
	name := testAccName("fw")
	ssh := `{ direction = "in", action = "ACCEPT", protocol = "tcp", source = "192.0.2.0/24", dport = "22", enabled = %t }`
	web := `{ direction = "in", action = "ACCEPT", protocol = "tcp", dport = "443" }`
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccFirewallConfig(name, fmt.Sprintf(ssh, true)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_firewall.test", tfjsonpath.New("rule"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue("pidginhost_firewall.test", tfjsonpath.New("status"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "pidginhost_firewall.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Rename, disable the first rule and add a second, in place.
				Config: testAccFirewallConfig(name+"-renamed", fmt.Sprintf(ssh, false), web),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_firewall.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_firewall.test", tfjsonpath.New("name"), knownvalue.StringExact(name+"-renamed")),
					statecheck.ExpectKnownValue("pidginhost_firewall.test", tfjsonpath.New("rule").AtSliceIndex(0).AtMapKey("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("pidginhost_firewall.test", tfjsonpath.New("rule").AtSliceIndex(1).AtMapKey("dport"), knownvalue.StringExact("443")),
				},
			},
		},
	})
}

func testAccFirewallConfig(name string, rules ...string) string {
	list := ""
	for _, r := range rules {
		list += "    " + r + ",\n"
	}
	return fmt.Sprintf(`
resource "pidginhost_firewall" "test" {
  name = %q
  rule = [
%s  ]
}
`, name, list)
}

func apiRule(id int32, dport string, enabled bool, position int32) pidginhost.FirewallRule {
	return pidginhost.FirewallRule{
		Id: id, Direction: "in", Action: "ACCEPT", Protocol: pidginhost.PtrString("tcp"), Source: pidginhost.PtrString(""),
		Dport: pidginhost.PtrString(dport), Enabled: &enabled, Position: &position,
	}
}

func wantRule(dport string, enabled *bool, position *int32) pidginhost.FirewallRuleRequest {
	return pidginhost.FirewallRuleRequest{
		Direction: "in", Action: "ACCEPT", Protocol: pidginhost.PtrString("tcp"), Dport: pidginhost.PtrString(dport),
		Enabled: enabled, Position: position,
	}
}

func TestDiffFirewallRules(t *testing.T) {
	existing := []pidginhost.FirewallRule{
		apiRule(1, "22", true, 10),
		apiRule(2, "80", true, 20),
		apiRule(3, "443", true, 30),
		apiRule(4, "443", true, 40),
	}
	desired := []pidginhost.FirewallRuleRequest{
		wantRule("22", pidginhost.PtrBool(true), pidginhost.PtrInt32(10)), // unchanged
		wantRule("443", pidginhost.PtrBool(false), nil),                   // first 443 rule, disabled
		wantRule("443", pidginhost.PtrBool(true), pidginhost.PtrInt32(5)), // second 443 rule, moved
		wantRule("8080", pidginhost.PtrBool(true), nil),                   // new
	}
	del, patch, create := diffFirewallRules(existing, desired)

	if !slices.Equal(del, []int32{2}) {
		t.Errorf("delete = %v, want [2]", del)
	}
	if len(patch) != 2 ||
		patch[0].id != 3 || patch[0].body.Enabled == nil || *patch[0].body.Enabled || patch[0].body.Position != nil ||
		patch[1].id != 4 || patch[1].body.Enabled != nil || patch[1].body.Position == nil || *patch[1].body.Position != 5 {
		t.Errorf("patch = %+v, want rule 3 disabled and rule 4 moved to 5", patch)
	}
	if len(create) != 1 || create[0].GetDport() != "8080" {
		t.Errorf("create = %+v, want the 8080 rule", create)
	}
}

func TestDiffFirewallRulesTreatsUnsetAsEmpty(t *testing.T) {
	existing := []pidginhost.FirewallRule{apiRule(1, "22", true, 10)}
	desired := []pidginhost.FirewallRuleRequest{wantRule("22", pidginhost.PtrBool(true), nil)}
	desired[0].Source = nil // the API answers "" for the unset source
	del, patch, create := diffFirewallRules(existing, desired)
	if len(del)+len(patch)+len(create) != 0 {
		t.Fatalf("got delete %v, patch %v, create %v; want no change", del, patch, create)
	}
}

func TestOrderFirewallRulesFollowsTheConfiguration(t *testing.T) {
	rule := func(dport string) firewallRuleModel {
		return firewallRuleModel{
			Direction: types.StringValue("in"), Action: types.StringValue("ACCEPT"), Protocol: types.StringValue("tcp"),
			Dport: types.StringValue(dport), Enabled: types.BoolValue(true), Position: types.Int64Unknown(),
		}
	}
	api := []pidginhost.FirewallRule{apiRule(1, "22", true, 10), apiRule(2, "80", true, 20), apiRule(3, "443", true, 30)}
	got := orderFirewallRules([]firewallRuleModel{rule("443"), rule("22")}, api)
	var dports []string
	for _, r := range got {
		dports = append(dports, r.Dport.ValueString())
	}
	if !slices.Equal(dports, []string{"443", "22", "80"}) {
		t.Fatalf("order = %v, want configured rules first, then the rest", dports)
	}
	if !got[0].Source.IsNull() || got[0].Position.ValueInt64() != 30 {
		t.Fatalf("rule = %+v, want a null source and position 30", got[0])
	}
	if orderFirewallRules(nil, nil) != nil || orderFirewallRules([]firewallRuleModel{}, nil) == nil {
		t.Fatal("an empty set must keep the configured null or empty list")
	}
}

func TestFirewallUpdateDeletesThenPatchesThenCreates(t *testing.T) {
	rules := []pidginhost.FirewallRule{apiRule(1, "22", true, 10), apiRule(2, "80", true, 20)}
	client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/firewall-rules-set/9/":
			return 200, pidginhost.FirewallRulesSet{Id: 9, Name: "web", Status: "validated", Rules: rules}
		case "DELETE /api/cloud/firewall-rules-set/9/rules/2/":
			return 204, nil
		case "PATCH /api/cloud/firewall-rules-set/9/rules/1/":
			return 200, rules[0]
		case "POST /api/cloud/firewall-rules-set/9/rules/":
			return 201, apiRule(3, "443", true, 30)
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &firewallResource{client: client}
	s := testSchema(r)
	rule := func(dport string, enabled bool) firewallRuleModel {
		return firewallRuleModel{
			Direction: types.StringValue("in"), Action: types.StringValue("ACCEPT"), Protocol: types.StringValue("tcp"),
			Dport: types.StringValue(dport), Enabled: types.BoolValue(enabled), Position: types.Int64Null(),
		}
	}
	state := firewallModel{ID: types.StringValue("9"), Name: types.StringValue("web"), Status: types.StringValue("validated"),
		Rules: []firewallRuleModel{rule("22", true), rule("80", true)}}
	plan := state
	plan.Status = types.StringUnknown()
	plan.Rules = []firewallRuleModel{rule("22", false), rule("443", true)}
	req := resource.UpdateRequest{State: testState(t, s, state), Plan: testPlan(t, s, plan)}
	resp := resource.UpdateResponse{State: testState(t, s, nil)}
	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	want := []string{
		"GET /api/cloud/firewall-rules-set/9/",
		"DELETE /api/cloud/firewall-rules-set/9/rules/2/",
		"PATCH /api/cloud/firewall-rules-set/9/rules/1/",
		"POST /api/cloud/firewall-rules-set/9/rules/",
		"GET /api/cloud/firewall-rules-set/9/",
	}
	if !slices.Equal(calls(), want) {
		t.Fatalf("calls = %v\nwant %v", calls(), want)
	}
}

func TestFirewallWaitsForValidationAndReportsRejectedRules(t *testing.T) {
	defer func(old time.Duration) { pollInterval = old }(pollInterval)
	pollInterval = time.Millisecond
	for _, tc := range []struct {
		final     pidginhost.FirewallRulesSetStatusEnum
		wantError string
	}{
		{"validated", ""},
		{"invalid", "port 70000 is out of range"},
	} {
		gets := 0
		client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
			rule := apiRule(3, "70000", true, 10)
			switch call {
			case "POST /api/cloud/firewall-rules-set/":
				return 201, pidginhost.FirewallRulesSet{Id: 9, Name: "web", Status: "pending", Rules: []pidginhost.FirewallRule{}}
			case "POST /api/cloud/firewall-rules-set/9/rules/":
				return 201, rule
			case "GET /api/cloud/firewall-rules-set/9/":
				// The API validates in the background: pending first.
				if gets++; gets == 1 {
					return 200, pidginhost.FirewallRulesSet{Id: 9, Name: "web", Status: "pending", Rules: []pidginhost.FirewallRule{rule}}
				}
				if tc.final == "invalid" {
					rule.HasError, rule.ErrorMessage = true, "port 70000 is out of range"
				}
				return 200, pidginhost.FirewallRulesSet{Id: 9, Name: "web", Status: tc.final, Rules: []pidginhost.FirewallRule{rule}}
			}
			return 500, map[string]any{"detail": "unexpected call " + call}
		})
		r := &firewallResource{client: client}
		s := testSchema(r)
		plan := firewallModel{ID: types.StringUnknown(), Name: types.StringValue("web"), Status: types.StringUnknown(),
			Rules: []firewallRuleModel{{
				Direction: types.StringValue("in"), Action: types.StringValue("ACCEPT"), Protocol: types.StringValue("tcp"),
				Dport: types.StringValue("70000"), Enabled: types.BoolValue(true), Position: types.Int64Null(),
			}}}
		resp := resource.CreateResponse{State: testState(t, s, nil)}
		r.Create(context.Background(), resource.CreateRequest{Plan: testPlan(t, s, plan)}, &resp)
		if tc.wantError != "" {
			if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), tc.wantError) {
				t.Fatalf("%s: diagnostics %v", tc.final, resp.Diagnostics)
			}
			continue
		}
		var got firewallModel
		resp.State.Get(context.Background(), &got)
		if resp.Diagnostics.HasError() || got.Status.ValueString() != "validated" || gets != 2 {
			t.Fatalf("%s: status %v gets %d diags %v calls %v", tc.final, got.Status, gets, resp.Diagnostics, calls())
		}
	}
}
