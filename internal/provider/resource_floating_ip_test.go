package provider

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccFloatingIP(t *testing.T) {
	server := testAccServerIDs(t, 1)[0]
	label := testAccName("fip")
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccFloatingIPConfig(label, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_floating_ip.test", tfjsonpath.New("address"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("pidginhost_floating_ip.test", tfjsonpath.New("label"), knownvalue.StringExact(label)),
				},
			},
			{
				ResourceName:      "pidginhost_floating_ip.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return "4/" + s.RootModule().Resources["pidginhost_floating_ip.test"].Primary.ID, nil
				},
			},
			{
				// Set the PTR and authorize a server in place.
				Config: testAccFloatingIPConfig(label, fmt.Sprintf(`
  reverse_dns           = "%s.example.com"
  authorized_server_ids = [%q]`, label, server)),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_floating_ip.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_floating_ip.test", tfjsonpath.New("reverse_dns"), knownvalue.StringExact(label+".example.com")),
					statecheck.ExpectKnownValue("pidginhost_floating_ip.test", tfjsonpath.New("authorized_server_ids"),
						knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact(server)})),
				},
			},
		},
	})
}

func testAccFloatingIPConfig(label, extra string) string {
	return fmt.Sprintf(`
resource "pidginhost_floating_ip" "test" {
  version = 4
  label   = %q%s
}
`, label, extra)
}

func TestDiffStrings(t *testing.T) {
	add, remove := diffStrings([]string{"5", "6"}, []string{"6", "7"})
	if !slices.Equal(add, []string{"7"}) || !slices.Equal(remove, []string{"5"}) {
		t.Fatalf("add %v, remove %v; want [7], [5]", add, remove)
	}
}

func TestFloatingIPUpdateAppliesTheExactServerSet(t *testing.T) {
	authorized := []int32{5, 6}
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/floating-ipv6/4/":
			return 200, pidginhost.FloatingIPv6{Id: 4, Address: "2001:db8::4", Gateway: "2001:db8::1", Prefix: 64,
				ReverseDns: "fip.example.com", Label: pidginhost.PtrString("lb"), AuthorizedVmCount: int32(len(authorized))}
		case "GET /api/cloud/floating-ipv6/4/authorizations/?page=1":
			var rows []pidginhost.FloatingIPAuthorization
			for i, id := range authorized {
				rows = append(rows, pidginhost.FloatingIPAuthorization{Id: int32(i), ServerId: id})
			}
			return 200, pidginhost.PaginatedFloatingIPAuthorizationList{Count: int32(len(rows)), Results: rows}
		case "POST /api/cloud/floating-ipv6/4/authorize/":
			authorized = append(authorized, int32(body["server_id"].(float64)))
			return 200, map[string]any{"authorized": true}
		case "POST /api/cloud/floating-ipv6/4/unauthorize/":
			id := int32(body["server_id"].(float64))
			authorized = slices.DeleteFunc(authorized, func(a int32) bool { return a == id })
			return 200, map[string]any{"unauthorized": true}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &floatingIPResource{client: client}
	s := testSchema(r)
	state := floatingIPModel{
		ID: types.StringValue("4"), Version: types.Int64Value(6), Label: types.StringValue("lb"),
		ReverseDNS: types.StringValue("fip.example.com"), AuthorizedServerIDs: []string{"5", "6"},
		Address: types.StringValue("2001:db8::4"), Gateway: types.StringValue("2001:db8::1"), Prefix: types.Int64Value(64),
	}
	plan := state
	plan.AuthorizedServerIDs = []string{"6", "7"}
	req := resource.UpdateRequest{State: testState(t, s, state), Plan: testPlan(t, s, plan)}
	resp := resource.UpdateResponse{State: testState(t, s, nil)}
	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	var got floatingIPModel
	resp.State.Get(context.Background(), &got)
	if !slices.Equal(got.AuthorizedServerIDs, []string{"6", "7"}) {
		t.Fatalf("authorized_server_ids = %v, want [6 7]; calls %v", got.AuthorizedServerIDs, calls())
	}
	if slices.ContainsFunc(calls(), func(c string) bool { return c == "POST /api/cloud/floating-ipv6/4/rdns/" }) {
		t.Fatalf("unchanged reverse DNS was sent: %v", calls())
	}
}
