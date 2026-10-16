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

// TestAccDNSZone activates FreeDNS for PIDGINHOST_ACC_DNS_DOMAIN, which
// points the domain's name servers at PidginHost until the test ends.
func TestAccDNSZone(t *testing.T) {
	domain := testAccDNSDomain(t)
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccDNSZoneConfig(domain, "192.0.2.10"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_dns_zone.test", tfjsonpath.New("id"), knownvalue.StringExact(fqdn(domain)+"/internal")),
				},
			},
			{
				ResourceName:            "pidginhost_dns_zone.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ip"},
			},
			{
				// ip only matters on activation: a new one updates the state alone.
				Config: testAccDNSZoneConfig(domain, "192.0.2.11"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_dns_zone.test", plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

func testAccDNSZoneConfig(domain, ip string) string {
	return fmt.Sprintf(`
resource "pidginhost_dns_zone" "test" {
  domain = %q
  ip     = %q
}
`, domain, ip)
}

func TestFindDNSZone(t *testing.T) {
	zones := []pidginhost.FreeDNSDomain{
		{Domain: "example.com", Active: false, Source: "internal"},
		{Domain: "example.com", Active: true, Source: "external"},
		{Domain: "Example.NET.", Active: true, Source: "internal"},
	}
	if z := findDNSZone(zones, "example.net", "internal"); z == nil || z.Domain != "Example.NET." {
		t.Errorf("example.net: got %v", z)
	}
	if z := findDNSZone(zones, "example.com.", "external"); z == nil || z.Source != "external" {
		t.Errorf("example.com external: got %v", z)
	}
	// An inactive zone and a zone from the other source do not count.
	if z := findDNSZone(zones, "example.com", "internal"); z != nil {
		t.Errorf("example.com internal: got %v, want none", z)
	}
}

func TestDNSZoneLifecycle(t *testing.T) {
	active := false
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "POST /api/freedns/dns/activate/":
			if body["domain"] != "example.com" || body["source"] != "internal" || body["ip"] != "192.0.2.10" {
				return 400, map[string]any{"detail": fmt.Sprint(body)}
			}
			active = true
			return 200, pidginhost.ActivateFreeDNSResponse{Message: "ok", Domain: "example.com", Source: "internal"}
		case "GET /api/freedns/dns/":
			return 200, []pidginhost.FreeDNSDomain{{Domain: "example.com", Active: active, Source: "internal"}}
		case "POST /api/freedns/dns/deactivate/":
			if body["domain"] != "example.com" || body["source"] != "internal" {
				return 400, map[string]any{"detail": fmt.Sprint(body)}
			}
			active = false
			return 200, pidginhost.DeactivateFreeDNSResponse{Message: "ok"}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &dnsZoneResource{client: client}
	s := testSchema(r)
	ctx := context.Background()

	plan := dnsZoneModel{ID: types.StringUnknown(), Domain: types.StringValue("Example.com."),
		Source: types.StringValue("internal"), IP: types.StringValue("192.0.2.10")}
	created := resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, plan)}, &created)
	var state dnsZoneModel
	created.State.Get(ctx, &state)
	if created.Diagnostics.HasError() || state.ID.ValueString() != "example.com/internal" || state.Domain.ValueString() != "Example.com." {
		t.Fatalf("create: %v %+v", created.Diagnostics, state)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || read.State.Raw.IsNull() {
		t.Fatalf("read: %v", read.Diagnostics)
	}

	// ip changes in the state only.
	plan = state
	plan.IP = types.StringValue("192.0.2.11")
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: testPlan(t, s, plan)}, &updated)
	updated.State.Get(ctx, &state)
	if updated.Diagnostics.HasError() || state.IP.ValueString() != "192.0.2.11" {
		t.Fatalf("update: %v %+v", updated.Diagnostics, state)
	}

	deleted := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("delete: %v", deleted.Diagnostics)
	}

	// The zone is gone: a refresh drops it.
	gone := resource.ReadResponse{State: updated.State}
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &gone)
	if gone.Diagnostics.HasError() || !gone.State.Raw.IsNull() {
		t.Fatalf("read after delete: %v, state kept", gone.Diagnostics)
	}
	want := []string{
		"POST /api/freedns/dns/activate/",
		"GET /api/freedns/dns/",
		"POST /api/freedns/dns/deactivate/",
		"GET /api/freedns/dns/",
	}
	if !slices.Equal(calls(), want) {
		t.Fatalf("calls = %v\nwant %v", calls(), want)
	}
}

func TestDNSZoneImportID(t *testing.T) {
	r := &dnsZoneResource{}
	s := testSchema(r)
	for id, ok := range map[string]bool{"example.com/internal": true, "example.com/external": true, "example.com": false, "example.com/other": false} {
		resp := resource.ImportStateResponse{State: testState(t, s, nil)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
		if resp.Diagnostics.HasError() == ok {
			t.Errorf("%q: diagnostics %v", id, resp.Diagnostics)
		}
	}
}
