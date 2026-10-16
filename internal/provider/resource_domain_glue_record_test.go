package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
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

// TestAccDomainGlueRecord registers a name server host under
// PIDGINHOST_ACC_DNS_DOMAIN; the registry must accept documentation addresses.
func TestAccDomainGlueRecord(t *testing.T) {
	domain := testAccDNSDomain(t)
	name := testAccName("ns")
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccDomainGlueRecordConfig(domain, name, `ip = "192.0.2.10"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_domain_glue_record.test", tfjsonpath.New("ip2"), knownvalue.Null()),
				},
			},
			{
				ResourceName:      "pidginhost_domain_glue_record.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testAccDomainGlueRecordConfig(domain, name, `ip = "192.0.2.11"
  ip2 = "2001:db8::10"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_domain_glue_record.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_domain_glue_record.test", tfjsonpath.New("ip"), knownvalue.StringExact("192.0.2.11")),
					statecheck.ExpectKnownValue("pidginhost_domain_glue_record.test", tfjsonpath.New("ip2"), knownvalue.StringExact("2001:db8::10")),
				},
			},
			{
				// Dropping ip2 clears it in place.
				Config: testAccDomainGlueRecordConfig(domain, name, `ip = "192.0.2.11"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_domain_glue_record.test", tfjsonpath.New("ip2"), knownvalue.Null()),
				},
			},
		},
	})
}

func testAccDomainGlueRecordConfig(domain, name, addresses string) string {
	return fmt.Sprintf(`
resource "pidginhost_domain_glue_record" "test" {
  domain = %q
  name   = %q
  %s
}
`, domain, name, addresses)
}

func TestFindGlue(t *testing.T) {
	records := []pidginhost.DNSGlue{{Name: "NS1", Ip: "192.0.2.1"}, {Name: "ns2", Ip: "192.0.2.2"}}
	if g := findGlue(records, "ns1"); g == nil || g.Name != "NS1" {
		t.Errorf("ns1: got %v", g)
	}
	if g := findGlue(records, "ns3"); g != nil {
		t.Errorf("ns3: got %v", g)
	}
}

func TestSameAddress(t *testing.T) {
	for _, pair := range [][2]string{{"2001:DB8:0::1", "2001:db8::1"}, {"192.0.2.1", "192.0.2.1"}, {"", ""}, {"x", "x"}} {
		if !sameAddress(pair[0], pair[1]) {
			t.Errorf("%q and %q differ", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"192.0.2.1", "192.0.2.2"}, {"192.0.2.1", ""}, {"X", "x"}} {
		if sameAddress(pair[0], pair[1]) {
			t.Errorf("%q and %q are the same", pair[0], pair[1])
		}
	}
}

func TestDomainGlueRecordFill(t *testing.T) {
	m := domainGlueRecordModel{IP: types.StringValue("2001:DB8::1"), IP2: types.StringValue("192.0.2.2")}
	m.fill(&pidginhost.DNSGlue{Name: "ns1", Ip: "2001:db8::1", Ip2: *pidginhost.NewNullableString(pidginhost.PtrString("192.0.2.2"))})
	if m.IP.ValueString() != "2001:DB8::1" || m.IP2.ValueString() != "192.0.2.2" {
		t.Errorf("same addresses: %+v", m)
	}
	m.fill(&pidginhost.DNSGlue{Name: "ns1", Ip: "192.0.2.9", Ip2: *pidginhost.NewNullableString(pidginhost.PtrString(""))})
	if m.IP.ValueString() != "192.0.2.9" || !m.IP2.IsNull() {
		t.Errorf("drift: %+v", m)
	}
	// After an import only the names are known.
	m = domainGlueRecordModel{}
	m.fill(&pidginhost.DNSGlue{Name: "ns1", Ip: "192.0.2.1", Ip2: *pidginhost.NewNullableString(pidginhost.PtrString("192.0.2.2"))})
	if m.IP.ValueString() != "192.0.2.1" || m.IP2.ValueString() != "192.0.2.2" {
		t.Errorf("import: %+v", m)
	}
}

func TestDomainGlueRecordLifecycle(t *testing.T) {
	// The API keeps the spelling a record was created with.
	records := []pidginhost.DNSGlue{{Name: "NS1", Ip: "192.0.2.1"}}
	var posted []map[string]any
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/domain/domain/example.com/dns/?page=1":
			return 200, map[string]any{"count": len(records), "next": "page2", "results": records[:1]}
		case "GET /api/domain/domain/example.com/dns/?page=2":
			return 200, map[string]any{"count": len(records), "next": nil, "results": records[1:]}
		case "POST /api/domain/domain/example.com/dns/":
			posted = append(posted, body)
			g := pidginhost.DNSGlue{Name: body["name"].(string), Ip: body["ip"].(string)}
			if i := slices.IndexFunc(records, func(r pidginhost.DNSGlue) bool { return r.Name == g.Name }); i >= 0 {
				records[i] = g
			} else {
				records = append(records, g)
			}
			return 200, g
		case "DELETE /api/domain/domain/example.com/dns/NS1/":
			records = records[1:]
			return 204, nil
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &domainGlueRecordResource{client: client}
	s := testSchema(r)
	ctx := context.Background()
	model := func(name, ip string) domainGlueRecordModel {
		return domainGlueRecordModel{ID: types.StringUnknown(), Domain: types.StringValue("Example.com"), Name: types.StringValue(name), IP: types.StringValue(ip)}
	}

	created := resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, model("ns2", "192.0.2.2"))}, &created)
	var state domainGlueRecordModel
	created.State.Get(ctx, &state)
	if created.Diagnostics.HasError() || state.ID.ValueString() != "example.com/ns2" || len(records) != 2 {
		t.Fatalf("create ns2: %v %+v %v", created.Diagnostics, state, records)
	}
	if b := posted[0]; b["name"] != "ns2" || b["ip"] != "192.0.2.2" || b["ip2"] != nil {
		t.Fatalf("create body %v", b)
	} else if _, sent := b["ip2"]; !sent {
		t.Fatalf("create body %v leaves ip2 out instead of clearing it", b)
	}

	// ns1 exists as NS1: creating it would overwrite a record Terraform does not manage.
	created = resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, model("ns1", "192.0.2.5"))}, &created)
	if !strings.Contains(fmt.Sprint(created.Diagnostics), "already has the glue record NS1") {
		t.Fatalf("create ns1: %v", created.Diagnostics)
	}

	// An imported ns1 updates in place under the stored spelling.
	imported := resource.ImportStateResponse{State: testState(t, s, nil)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "example.com/ns1"}, &imported)
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	read.State.Get(ctx, &state)
	if read.Diagnostics.HasError() || state.IP.ValueString() != "192.0.2.1" || !state.IP2.IsNull() || state.Name.ValueString() != "ns1" {
		t.Fatalf("import ns1: %v %+v", read.Diagnostics, state)
	}
	plan := state
	plan.IP = types.StringValue("192.0.2.9")
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: testPlan(t, s, plan)}, &updated)
	if updated.Diagnostics.HasError() || posted[len(posted)-1]["name"] != "NS1" || len(records) != 2 || records[0].Ip != "192.0.2.9" {
		t.Fatalf("update: %v, posted %v, records %v", updated.Diagnostics, posted, records)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})
	if !slices.Contains(calls(), "DELETE /api/domain/domain/example.com/dns/NS1/") {
		t.Fatalf("delete: calls %v", calls())
	}
	gone := resource.ReadResponse{State: updated.State}
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &gone)
	if gone.Diagnostics.HasError() || !gone.State.Raw.IsNull() {
		t.Fatalf("read after delete: %v, state kept", gone.Diagnostics)
	}
}
