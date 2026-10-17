package provider

import (
	"context"
	"fmt"
	"os"
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
)

// TestAccDomainNameservers sets the name servers of PIDGINHOST_ACC_DNS_DOMAIN
// to PIDGINHOST_ACC_DNS_NAMESERVERS, a comma separated list of the name
// servers the domain already has, so the test leaves the domain working.
func TestAccDomainNameservers(t *testing.T) {
	domain := testAccDNSDomain(t)
	nameservers := splitNameservers(os.Getenv("PIDGINHOST_ACC_DNS_NAMESERVERS"))
	if len(nameservers) < 2 {
		t.Skip("set PIDGINHOST_ACC_DNS_NAMESERVERS to the name servers the test domain has")
	}
	reversed := slices.Clone(nameservers)
	slices.Reverse(reversed)
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccDomainNameserversConfig(domain, nameservers),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_domain_nameservers.test", tfjsonpath.New("nameservers"), knownvalue.SetSizeExact(len(nameservers))),
				},
			},
			{
				ResourceName:      "pidginhost_domain_nameservers.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// The name servers are a set: their order is no change.
				Config: testAccDomainNameserversConfig(domain, reversed),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccDomainNameserversConfig(domain string, nameservers []string) string {
	return fmt.Sprintf(`
resource "pidginhost_domain_nameservers" "test" {
  domain      = %q
  nameservers = ["%s"]
}
`, domain, strings.Join(nameservers, `", "`))
}

func TestSplitNameservers(t *testing.T) {
	if got := splitNameservers(" ns1.example.com, NS2.example.com.,,"); !slices.Equal(got, []string{"ns1.example.com", "NS2.example.com."}) {
		t.Errorf("got %q", got)
	}
	if got := splitNameservers(""); got != nil {
		t.Errorf("empty: got %q", got)
	}
}

func TestSameNameservers(t *testing.T) {
	a := []string{"ns1.example.com", "ns2.example.com"}
	for _, b := range [][]string{
		{"NS2.example.com.", "ns1.example.com"},
		{"ns1.example.com", "ns2.example.com", "ns1.example.com."},
	} {
		if !sameNameservers(a, b) {
			t.Errorf("%q differs from %q", b, a)
		}
	}
	for _, b := range [][]string{
		{"ns1.example.com"},
		{"ns1.example.com", "ns3.example.com"},
		{"ns1.example.com", "ns2.example.com", "ns3.example.com"},
	} {
		if sameNameservers(a, b) {
			t.Errorf("%q equals %q", b, a)
		}
	}
}

// testDomain is a domain as the API returns it, contacts included.
func testDomain(nameservers string) map[string]any {
	return map[string]any{
		"id": 5, "domain": "example.com", "idna": false, "nameservers": nameservers,
		"tld":             map[string]any{"id": 1, "tld": "com", "price": "10.00", "registrar": "test"},
		"expiration_date": "2030-01-01", "registration_date": "2020-01-01", "service": nil, "idna_name": "example.com",
		"max_renew_years": 9, "service_status": "active", "contacts": map[string]any{"owner": map[string]any{"name": "Jane Doe"}},
	}
}

func TestDomainNameserversLifecycle(t *testing.T) {
	current := "ns1.example.net,ns2.example.net"
	var posted []any
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/domain/domain/example.com/":
			return 200, testDomain(current)
		case "POST /api/domain/domain/example.com/nameservers/":
			posted = body["nameservers"].([]any)
			return 200, map[string]any{"message": "ok"}
		case "GET /api/domain/domain/example.org/":
			return 404, map[string]any{"detail": "Not found."}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &domainNameserversResource{client: client}
	s := testSchema(r)
	ctx := context.Background()
	model := func(domain string, names ...string) domainNameserversModel {
		m := domainNameserversModel{ID: types.StringUnknown(), Domain: types.StringValue(domain)}
		for _, ns := range names {
			m.Nameservers = append(m.Nameservers, types.StringValue(ns))
		}
		return m
	}

	// The same set spelled differently needs no registry update.
	created := resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, model("Example.com.", "NS2.example.net.", "ns1.example.net"))}, &created)
	var state domainNameserversModel
	created.State.Get(ctx, &state)
	if created.Diagnostics.HasError() || state.ID.ValueString() != "example.com" || posted != nil {
		t.Fatalf("create: %v %+v, posted %v", created.Diagnostics, state, posted)
	}

	// A refresh keeps the configured spelling, then shows a change made elsewhere.
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	read.State.Get(ctx, &state)
	if read.Diagnostics.HasError() || state.names()[0] != "NS2.example.net." {
		t.Fatalf("read: %v %+v", read.Diagnostics, state)
	}
	current = "ns1.example.net,ns3.example.net"
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	read.State.Get(ctx, &state)
	if !slices.Equal(state.names(), []string{"ns1.example.net", "ns3.example.net"}) {
		t.Fatalf("drift: %q", state.names())
	}

	plan := model("example.com", "ns1.example.net", "NS2.Example.net.")
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: testPlan(t, s, plan)}, &updated)
	if updated.Diagnostics.HasError() || fmt.Sprint(posted) != "[ns1.example.net ns2.example.net]" {
		t.Fatalf("update: %v, posted %v", updated.Diagnostics, posted)
	}

	before := len(calls())
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})
	if len(calls()) != before {
		t.Fatalf("delete called the API: %v", calls()[before:])
	}

	// A domain the account does not have: refused on create, dropped on refresh.
	created = resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, model("example.org", "ns1.example.net", "ns2.example.net"))}, &created)
	if !strings.Contains(fmt.Sprint(created.Diagnostics), "not registered on this account") {
		t.Fatalf("missing domain: %v", created.Diagnostics)
	}
	gone := model("example.org", "ns1.example.net", "ns2.example.net")
	gone.ID = types.StringValue("example.org")
	goneState := testState(t, s, gone)
	read = resource.ReadResponse{State: goneState}
	r.Read(ctx, resource.ReadRequest{State: goneState}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("read missing domain: %v, state kept", read.Diagnostics)
	}
}

func TestDomainErrorsWithholdTheResponse(t *testing.T) {
	client, _ := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		if call == "GET /api/domain/domain/example.com/" {
			// A body that does not decode would be quoted by the SDK.
			return 200, map[string]any{"contacts": map[string]any{"owner": "Jane Doe"}}
		}
		return 400, map[string]any{"nameservers": []string{"Jane Doe owns this domain"}}
	})
	r := &domainNameserversResource{client: client}
	_, _, diags := r.nameservers(context.Background(), "example.com")
	diags.Append(r.set(context.Background(), "example.com", []string{"ns1.example.net", "ns2.example.net"})...)
	if diags.ErrorsCount() != 2 {
		t.Fatalf("want two errors, got %v", diags)
	}
	for _, d := range diags {
		if strings.Contains(d.Detail(), "Jane") || !strings.Contains(d.Detail(), "personal data") {
			t.Errorf("detail %q", d.Detail())
		}
	}
}
