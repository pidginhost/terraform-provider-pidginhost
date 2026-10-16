package provider

import (
	"context"
	"fmt"
	"slices"
	"testing"

	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccPublicIP(t *testing.T) {
	server := testAccServerIDs(t, 1)[0]
	rdns := testAccName("ip") + ".example.com"
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: `resource "pidginhost_public_ip" "test" { version = 4 }`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_public_ip.test", tfjsonpath.New("address"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("pidginhost_public_ip.test", tfjsonpath.New("server_id"), knownvalue.Null()),
				},
			},
			{
				ResourceName:      "pidginhost_public_ip.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return "4/" + s.RootModule().Resources["pidginhost_public_ip.test"].Primary.ID, nil
				},
			},
			{
				// Attach and set the PTR in place.
				Config: fmt.Sprintf(`
resource "pidginhost_public_ip" "test" {
  version     = 4
  server_id   = %q
  reverse_dns = %q
}
`, server, rdns),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_public_ip.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_public_ip.test", tfjsonpath.New("server_id"), knownvalue.StringExact(server)),
					statecheck.ExpectKnownValue("pidginhost_public_ip.test", tfjsonpath.New("reverse_dns"), knownvalue.StringExact(rdns)),
				},
			},
		},
	})
}

func TestPlanIPBinding(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		bound                  bool
		boundTo, managed, want string
		detach, attach, refuse bool
	}{
		{name: "unbound, stays so"},
		{name: "unbound, attach", want: "7", attach: true},
		{name: "already there", bound: true, boundTo: "7", managed: "5", want: "7"},
		{name: "move", bound: true, boundTo: "5", managed: "5", want: "7", detach: true, attach: true},
		{name: "detach", bound: true, boundTo: "5", managed: "5", detach: true},
		{name: "foreign server, move", bound: true, boundTo: "9", managed: "5", want: "7", refuse: true},
		{name: "foreign server, detach", bound: true, boundTo: "9", managed: "5", refuse: true},
		{name: "bound without a server id", bound: true, managed: "5", want: "7", refuse: true},
	} {
		detach, attach, err := planIPBinding(tc.bound, tc.boundTo, tc.managed, tc.want)
		if detach != tc.detach || attach != tc.attach || (err != nil) != tc.refuse {
			t.Errorf("%s: got detach %v, attach %v, err %v", tc.name, detach, attach, err)
		}
	}
}

func TestPublicIPMoveWaitsForTheDetach(t *testing.T) {
	ip := pidginhost.PublicIPv6{Id: 3, Address: "2001:db8::10", Gateway: "2001:db8::1", Prefix: 64, Attached: true,
		Server: "web-5", ServerId: *pidginhost.NewNullableInt32(pidginhost.PtrInt32(5))}
	detaching := 0
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/ipv6/3/":
			if detaching > 0 {
				if detaching--; detaching == 0 {
					ip.Attached, ip.Server = false, ""
					ip.ServerId.Unset()
				}
			}
			return 200, ip
		case "POST /api/cloud/ipv6/3/detach/":
			detaching = 2
			return 200, map[string]any{"detached": true}
		case "POST /api/cloud/servers/7/attach-ipv6/":
			if ip.ServerId.Get() != nil || body["ipv6"] != ip.Address {
				return 400, map[string]any{"detail": "address is attached"}
			}
			ip.ServerId.Set(pidginhost.PtrInt32(7))
			return 200, map[string]any{"attached": true, "reboot_required": true, "rebooted": false}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &publicIPResource{client: client}
	current := pidginhost.PublicIPv4(ip)
	rebootNeeded, err := r.bind(context.Background(), 6, &current, "5", "7", false)
	if err != nil || !rebootNeeded {
		t.Fatalf("got reboot needed %v, err %v; calls %v", rebootNeeded, err, calls())
	}
	if !slices.Contains(calls(), "POST /api/cloud/servers/7/attach-ipv6/") {
		t.Fatalf("no attach call: %v", calls())
	}
}
