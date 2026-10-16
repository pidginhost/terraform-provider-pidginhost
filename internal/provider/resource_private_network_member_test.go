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
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccPrivateNetworkMember(t *testing.T) {
	server := testAccServerIDs(t, 1)[0]
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccPrivateNetworkMemberConfig(server, "203.0.113.10"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_private_network_member.test", tfjsonpath.New("address"), knownvalue.StringExact("203.0.113.10")),
				},
			},
			{
				ResourceName:      "pidginhost_private_network_member.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Every attribute is fixed at creation: a new address replaces the membership.
				Config: testAccPrivateNetworkMemberConfig(server, "203.0.113.11"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_private_network_member.test", plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_private_network_member.test", tfjsonpath.New("address"), knownvalue.StringExact("203.0.113.11")),
				},
			},
		},
	})
}

func testAccPrivateNetworkMemberConfig(server, address string) string {
	return fmt.Sprintf(`
resource "pidginhost_private_network" "test" {
  address = "203.0.113.0/24"
}

resource "pidginhost_private_network_member" "test" {
  network_id = pidginhost_private_network.test.id
  server_id  = %q
  address    = %q
}
`, server, address)
}

func TestPrivateNetworkMemberDeleteSendsThePrivateAddress(t *testing.T) {
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/private-networks/3/":
			return 200, pidginhost.PrivateNetwork{Id: 3, Slug: "203.0.113.0/24", Address: "203.0.113.0/24"}
		case "GET /api/cloud/servers/8/":
			private := []pidginhost.ServerPrivateInterface{
				{Interface: "eth1", Address: "198.51.100.5", Network: "198.51.100.0/24"},
				{Interface: "eth2", Address: "203.0.113.5", Network: "203.0.113.0/24"},
			}
			return 200, pidginhost.ServerDetail{Id: 8, Hostname: "db", Status: "active", Networks: pidginhost.ServerNetworks{Private: private}}
		case "POST /api/cloud/private-networks/3/remove-server/":
			if body["server"] != "203.0.113.5" {
				return 400, map[string]any{"server": "not a member"}
			}
			return 200, map[string]any{"removed": true}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &privateNetworkMemberResource{client: client}
	s := testSchema(r)
	state := privateNetworkMemberModel{ID: types.StringValue("3/8"), NetworkID: types.StringValue("3"),
		ServerID: types.StringValue("8"), Address: types.StringValue("203.0.113.5")}
	resp := resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: testState(t, s, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	if !slices.Contains(calls(), "POST /api/cloud/private-networks/3/remove-server/") {
		t.Fatalf("no remove call: %v", calls())
	}
}
