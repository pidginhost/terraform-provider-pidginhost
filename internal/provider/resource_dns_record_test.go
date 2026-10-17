package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

// TestAccDNSRecord activates FreeDNS for PIDGINHOST_ACC_DNS_DOMAIN and adds
// records of several types to it.
func TestAccDNSRecord(t *testing.T) {
	domain := testAccDNSDomain(t)
	label := testAccName("rec")
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccDNSRecordConfig(domain, label, "192.0.2.10", 3600),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_dns_record.a", tfjsonpath.New("ttl"), knownvalue.Int64Exact(3600)),
					statecheck.ExpectKnownValue("pidginhost_dns_record.mx", tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("pidginhost_dns_record.txt", tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("pidginhost_dns_record.caa", tfjsonpath.New("id"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "pidginhost_dns_record.a",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s/internal/%s/A/192.0.2.10", domain, label),
				ImportStateVerify: true,
			},
			{
				ResourceName:      "pidginhost_dns_record.mx",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s/internal/%s/MX", domain, label),
				ImportStateVerify: true,
			},
			{
				// ttl changes in place.
				Config: testAccDNSRecordConfig(domain, label, "192.0.2.10", 7200),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_dns_record.a", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_dns_record.a", tfjsonpath.New("ttl"), knownvalue.Int64Exact(7200)),
				},
			},
			{
				// A new value is a new record.
				Config: testAccDNSRecordConfig(domain, label, "192.0.2.11", 7200),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_dns_record.a", plancheck.ResourceActionReplace)},
				},
			},
		},
	})
}

func testAccDNSRecordConfig(domain, label, address string, ttl int) string {
	return fmt.Sprintf(`
resource "pidginhost_dns_zone" "test" {
  domain = %[1]q
  ip     = "192.0.2.1"
}

resource "pidginhost_dns_record" "a" {
  zone    = pidginhost_dns_zone.test.domain
  name    = %[2]q
  type    = "A"
  ttl     = %[4]d
  address = %[3]q
}

resource "pidginhost_dns_record" "mx" {
  zone       = pidginhost_dns_zone.test.domain
  name       = %[2]q
  type       = "MX"
  preference = 10
  exchange   = "mail.example.com"
}

resource "pidginhost_dns_record" "txt" {
  zone    = pidginhost_dns_zone.test.domain
  name    = %[2]q
  type    = "TXT"
  txtdata = "v=spf1 -all"
}

resource "pidginhost_dns_record" "caa" {
  zone  = pidginhost_dns_zone.test.domain
  name  = %[2]q
  type  = "CAA"
  flag  = 0
  tag   = "issue"
  value = "letsencrypt.org"
}
`, domain, label, address, ttl)
}

func TestDNSRecordName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"", "example.com."},
		{" @ ", "example.com."},
		{"www", "www.example.com."},
		{"WWW", "www.example.com."},
		{"www.example.com", "www.example.com."},
		{"Www.Example.com.", "www.example.com."},
		{"example.com", "example.com."},
		{"mail.example.net.", "mail.example.net."},
		{"example.net", "example.net.example.com."},
		{"notexample.com", "notexample.com.example.com."},
		{"_dmarc", "_dmarc.example.com."},
		{"bücher", "xn--bcher-kva.example.com."},
	} {
		if got := dnsRecordName(tc.name, "example.com"); got != tc.want {
			t.Errorf("dnsRecordName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeDNSValue(t *testing.T) {
	for _, tc := range []struct{ field, value, want string }{
		{"cname", "Target.Example.com.", "target.example.com"},
		{"exchange", "mail.example.com", "mail.example.com"},
		{"target", "SIP.example.com..", "sip.example.com"},
		{"address", "2001:DB8:0:0::1", "2001:db8::1"},
		{"address", "192.0.2.10", "192.0.2.10"},
		{"address", "not an address", "not an address"},
		{"txtdata", "Hello World.", "Hello World."},
		{"value", "LetsEncrypt.org", "LetsEncrypt.org"},
	} {
		if got := normalizeDNSValue(tc.field, tc.value); got != tc.want {
			t.Errorf("normalizeDNSValue(%q, %q) = %q, want %q", tc.field, tc.value, got, tc.want)
		}
	}
}

func testDNSRecords() []pidginhost.DNSRecord {
	return []pidginhost.DNSRecord{
		{Line: 1, Name: "example.com.", Ttl: 86400, Type: "SOA"},
		{Line: 2, Name: "www.example.com.", Ttl: 14400, Type: "A", Address: pidginhost.PtrString("192.0.2.10")},
		{Line: 3, Name: "WWW.example.com.", Ttl: 14400, Type: "A", Address: pidginhost.PtrString("192.0.2.11")},
		{Line: 4, Name: "example.com.", Ttl: 14400, Type: "MX", Preference: pidginhost.PtrInt32(10), Exchange: pidginhost.PtrString("Mail.Example.com")},
		{Line: 5, Name: "example.com.", Ttl: 14400, Type: "TYPE257", Flag: pidginhost.PtrInt32(0), Tag: pidginhost.PtrString("issue"), Value: pidginhost.PtrString("letsencrypt.org")},
		{Line: 6, Name: "_sip._tcp.example.com.", Ttl: 14400, Type: "SRV", Priority: pidginhost.PtrInt32(10), Weight: pidginhost.PtrInt32(5),
			Port: pidginhost.PtrInt32(5060), Target: pidginhost.PtrString("sip.example.com")},
	}
}

func TestMatchDNSRecords(t *testing.T) {
	records := testDNSRecords()
	lines := func(found []pidginhost.DNSRecord) []int32 {
		var out []int32
		for _, r := range found {
			out = append(out, r.Line)
		}
		return out
	}
	for _, tc := range []struct {
		desc, fqname, rtype string
		values              map[string]string
		want                []int32
	}{
		{"name and type only", "www.example.com.", "A", nil, []int32{2, 3}},
		{"address", "www.example.com.", "A", map[string]string{"address": "192.0.2.11"}, []int32{3}},
		{"host name without case and dot", "example.com.", "MX", map[string]string{"preference": "10", "exchange": "mail.example.com."}, []int32{4}},
		{"other preference", "example.com.", "MX", map[string]string{"preference": "20", "exchange": "mail.example.com"}, nil},
		{"CAA listed as TYPE257", "example.com.", "CAA", map[string]string{"flag": "0", "tag": "issue", "value": "letsencrypt.org"}, []int32{5}},
		{"SRV", "_sip._tcp.example.com.", "SRV", map[string]string{"priority": "10", "weight": "5", "port": "5060", "target": "SIP.example.com."}, []int32{6}},
		{"other type", "www.example.com.", "AAAA", nil, nil},
		{"fields of other types do not count", "www.example.com.", "A", map[string]string{"address": "192.0.2.10", "cname": "x"}, []int32{2}},
	} {
		if got := lines(matchDNSRecords(records, "example.com", tc.fqname, tc.rtype, tc.values)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: lines %v, want %v", tc.desc, got, tc.want)
		}
	}
}

func TestDNSFieldProblems(t *testing.T) {
	missing, extra := dnsFieldProblems("MX", map[string]bool{"exchange": true, "address": true, "cname": true, "preference": false})
	if !slices.Equal(missing, []string{"preference"}) || !slices.Equal(extra, []string{"address", "cname"}) {
		t.Errorf("MX: missing %v, extra %v", missing, extra)
	}
	missing, extra = dnsFieldProblems("CAA", map[string]bool{"flag": true, "tag": true, "value": true})
	if missing != nil || extra != nil {
		t.Errorf("CAA: missing %v, extra %v", missing, extra)
	}
}

func TestParseDNSRecordImportID(t *testing.T) {
	for _, tc := range []struct {
		id, name, rtype string
		values          map[string]string
	}{
		{"example.com/internal/www/a", "www", "A", nil},
		{"example.com/external/@/MX/10 mail.example.com", "@", "MX", map[string]string{"preference": "10", "exchange": "mail.example.com"}},
		{"example.com/internal/_dmarc/TXT/v=DMARC1; p=none; rua=mailto:a@example.com/x", "_dmarc", "TXT",
			map[string]string{"txtdata": "v=DMARC1; p=none; rua=mailto:a@example.com/x"}},
		{"example.com/internal//CAA/0 issue letsencrypt.org; validationmethods=dns-01", "", "CAA",
			map[string]string{"flag": "0", "tag": "issue", "value": "letsencrypt.org; validationmethods=dns-01"}},
	} {
		m, values, err := parseDNSRecordImportID(tc.id)
		if err != nil || m.Zone.ValueString() != "example.com" || m.Name.ValueString() != tc.name || m.Type.ValueString() != tc.rtype ||
			fmt.Sprint(values) != fmt.Sprint(tc.values) || (values == nil) != (tc.values == nil) {
			t.Errorf("%q: got %+v, %v, %v", tc.id, m, values, err)
		}
	}
	for _, bad := range []string{
		"example.com/internal/www", "example.com/other/www/A", "example.com/internal/www/NS",
		"example.com/internal/@/MX/10", "/internal/www/A",
	} {
		if _, _, err := parseDNSRecordImportID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDNSRecordIDIgnoresSpelling(t *testing.T) {
	a := dnsRecordModel{Zone: types.StringValue("Example.com."), Source: types.StringValue("internal"), Name: types.StringValue("@"),
		Type: types.StringValue("MX"), Preference: types.Int64Value(10), Exchange: types.StringValue("Mail.Example.com.")}
	b := dnsRecordModel{Zone: types.StringValue("example.com"), Source: types.StringValue("internal"), Name: types.StringValue("example.com."),
		Type: types.StringValue("MX"), Preference: types.Int64Value(10), Exchange: types.StringValue("mail.example.com")}
	if a.id() != b.id() || !strings.HasPrefix(a.id().ValueString(), "example.com/internal/example.com/MX/") {
		t.Fatalf("ids %s and %s", a.id(), b.id())
	}
	b.Preference = types.Int64Value(20)
	if a.id() == b.id() {
		t.Fatal("another value kept the id")
	}
}

func TestDNSRecordValidateConfig(t *testing.T) {
	r := &dnsRecordResource{}
	s := testSchema(r)
	validate := func(m dnsRecordModel) diag.Diagnostics {
		resp := resource.ValidateConfigResponse{}
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: testState(t, s, m).Raw}}, &resp)
		return resp.Diagnostics
	}
	mx := dnsRecordModel{Zone: types.StringValue("example.com"), Name: types.StringValue("@"), Type: types.StringValue("MX"),
		Exchange: types.StringValue("mail.example.com"), Address: types.StringValue("192.0.2.10")}
	diags := validate(mx)
	if diags.ErrorsCount() != 2 || !strings.Contains(fmt.Sprint(diags), "requires preference") || !strings.Contains(fmt.Sprint(diags), "do not use address") {
		t.Errorf("MX: %v", diags)
	}
	// A value known only at apply counts as set.
	a := dnsRecordModel{Zone: types.StringValue("example.com"), Name: types.StringValue("www"), Type: types.StringValue("A"), Address: types.StringUnknown()}
	if diags := validate(a); diags.HasError() {
		t.Errorf("A: %v", diags)
	}
}

// fakeZoneRecords is a FreeDNS zone of example.com that numbers its records
// by line, renumbering them after a delete as the API does.
type fakeZoneRecords struct{ records []pidginhost.DNSRecord }

func (z *fakeZoneRecords) handle(call string, body map[string]any) (int, any) {
	const query = "?domain=example.com&source=internal"
	switch call {
	case "GET /api/freedns/dns/records/" + query:
		return 200, z.records
	case "POST /api/freedns/dns/add-record/" + query:
		var rec pidginhost.DNSRecord
		body["Line"] = body["line"]
		raw, _ := json.Marshal(body)
		if err := json.Unmarshal(raw, &rec); err != nil {
			return 400, map[string]any{"detail": err.Error()}
		}
		if body["line"] == nil {
			rec.Line = int32(len(z.records) + 1)
			z.records = append(z.records, rec)
		} else {
			z.records[rec.Line-1] = rec
		}
		return 200, pidginhost.DNSRecordMutateResponse{Message: "ok", Record: &rec}
	case "POST /api/freedns/dns/delete-record/" + query:
		line := int(body["line"].(float64))
		z.records = slices.Delete(z.records, line-1, line)
		for i := range z.records {
			z.records[i].Line = int32(i + 1)
		}
		return 200, pidginhost.DeleteRecordResponse{Message: "ok", Line: int32(line)}
	}
	return 500, map[string]any{"detail": "unexpected call " + call}
}

func TestDNSRecordLifecycle(t *testing.T) {
	zone := &fakeZoneRecords{records: testDNSRecords()[:2]} // SOA and www A
	var bodies []map[string]any
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		if body != nil {
			bodies = append(bodies, body)
		}
		return zone.handle(call, body)
	})
	r := &dnsRecordResource{client: client}
	s := testSchema(r)
	ctx := context.Background()
	record := func(name, rtype string) dnsRecordModel {
		return dnsRecordModel{ID: types.StringUnknown(), Zone: types.StringValue("example.com"), Source: types.StringValue("internal"),
			Name: types.StringValue(name), Type: types.StringValue(rtype), TTL: types.Int64Value(14400)}
	}
	create := func(plan dnsRecordModel) (tfsdk.State, diag.Diagnostics) {
		resp := resource.CreateResponse{State: testState(t, s, nil)}
		r.Create(ctx, resource.CreateRequest{Plan: testPlan(t, s, plan)}, &resp)
		return resp.State, resp.Diagnostics
	}

	mx := record("@", "MX")
	mx.Preference, mx.Exchange = types.Int64Value(10), types.StringValue("Mail.Example.com.")
	mxState, diags := create(mx)
	if diags.HasError() {
		t.Fatalf("create MX: %v\ncalls %v", diags, calls())
	}
	if b := bodies[len(bodies)-1]; b["name"] != "example.com." || b["type"] != "MX" || b["preference"] != float64(10) ||
		b["exchange"] != "Mail.Example.com." || b["ttl"] != float64(14400) || b["line"] != nil {
		t.Fatalf("MX body %v", b)
	}
	txt := record("_dmarc", "TXT")
	txt.Txtdata = types.StringValue("v=DMARC1; p=none")
	if _, diags := create(txt); diags.HasError() || bodies[len(bodies)-1]["unencoded"] != "1" || bodies[len(bodies)-1]["name"] != "_dmarc.example.com." {
		t.Fatalf("create TXT: %v, body %v", diags, bodies[len(bodies)-1])
	}
	caa := record("@", "CAA")
	caa.Flag, caa.Tag, caa.Value = types.Int64Value(0), types.StringValue("issue"), types.StringValue("letsencrypt.org")
	if _, diags := create(caa); diags.HasError() || bodies[len(bodies)-1]["type"] != "TYPE257" {
		t.Fatalf("create CAA: %v, body %v", diags, bodies[len(bodies)-1])
	}
	// The same record again, spelled differently, already exists.
	dup := mx
	dup.Name, dup.Exchange = types.StringValue("example.com"), types.StringValue("mail.example.com")
	if _, diags := create(dup); !strings.Contains(fmt.Sprint(diags), "already exists") {
		t.Fatalf("duplicate MX: %v", diags)
	}

	// Deleting the www record moves the MX record from line 3 to line 2.
	www := record("www", "A")
	www.Address, www.ID = types.StringValue("192.0.2.10"), types.StringValue("x")
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: testState(t, s, www)}, &del)
	if del.Diagnostics.HasError() || bodies[len(bodies)-1]["line"] != float64(2) {
		t.Fatalf("delete www: %v, body %v", del.Diagnostics, bodies[len(bodies)-1])
	}

	// ttl changes in place, on the line the record has now.
	var state dnsRecordModel
	mxState.Get(ctx, &state)
	plan := state
	plan.TTL = types.Int64Value(300)
	upd := resource.UpdateResponse{State: mxState}
	r.Update(ctx, resource.UpdateRequest{State: mxState, Plan: testPlan(t, s, plan)}, &upd)
	if b := bodies[len(bodies)-1]; upd.Diagnostics.HasError() || b["line"] != float64(2) || b["ttl"] != float64(300) || b["exchange"] != "Mail.Example.com." {
		t.Fatalf("update: %v, body %v", upd.Diagnostics, b)
	}

	// A refresh keeps the configured spelling and reads the ttl.
	zone.records[1].Ttl = 600
	read := resource.ReadResponse{State: upd.State}
	r.Read(ctx, resource.ReadRequest{State: upd.State}, &read)
	read.State.Get(ctx, &state)
	if read.Diagnostics.HasError() || state.TTL.ValueInt64() != 600 || state.Exchange.ValueString() != "Mail.Example.com." || state.ID != mx.id() {
		t.Fatalf("read: %v %+v", read.Diagnostics, state)
	}

	del = resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
	if del.Diagnostics.HasError() || bodies[len(bodies)-1]["line"] != float64(2) {
		t.Fatalf("delete MX: %v, body %v", del.Diagnostics, bodies[len(bodies)-1])
	}
	gone := resource.ReadResponse{State: read.State}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &gone)
	if gone.Diagnostics.HasError() || !gone.State.Raw.IsNull() {
		t.Fatalf("read after delete: %v, state kept", gone.Diagnostics)
	}
	for _, rec := range zone.records {
		if rec.Type == "MX" || rec.Type == "A" {
			t.Fatalf("left %+v in the zone", rec)
		}
	}
}

func TestDNSRecordImport(t *testing.T) {
	zone := &fakeZoneRecords{records: testDNSRecords()}
	client, _ := fakeAPI(t, func(call string, body map[string]any) (int, any) { return zone.handle(call, body) })
	r := &dnsRecordResource{client: client}
	s := testSchema(r)
	importID := func(id string) (dnsRecordModel, diag.Diagnostics) {
		resp := resource.ImportStateResponse{State: testState(t, s, nil)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
		var m dnsRecordModel
		resp.State.Get(context.Background(), &m)
		return m, resp.Diagnostics
	}
	m, diags := importID("example.com/internal/@/MX")
	if diags.HasError() || m.Preference.ValueInt64() != 10 || m.Exchange.ValueString() != "mail.example.com" ||
		m.TTL.ValueInt64() != 14400 || !m.Address.IsNull() || m.Name.ValueString() != "@" {
		t.Fatalf("MX: %v %+v", diags, m)
	}
	if _, diags := importID("example.com/internal/www/A"); !strings.Contains(fmt.Sprint(diags), "has 2 A records") {
		t.Fatalf("ambiguous A: %v", diags)
	}
	if m, diags := importID("example.com/internal/www/A/192.0.2.11"); diags.HasError() || m.Address.ValueString() != "192.0.2.11" {
		t.Fatalf("A by address: %v %+v", diags, m)
	}
	if _, diags := importID("example.com/internal/www/AAAA"); !strings.Contains(fmt.Sprint(diags), "not found") {
		t.Fatalf("missing AAAA: %v", diags)
	}
}

func TestDNSRecordLeavesStateWhenItsZoneIsDeactivated(t *testing.T) {
	for _, tc := range []struct {
		zoneActive bool
		wantGone   bool
	}{{false, true}, {true, false}} {
		client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
			switch call {
			case "GET /api/freedns/dns/records/?domain=example.com&source=internal":
				// The API refuses to list the records of an inactive zone.
				return 400, map[string]any{"message": "FreeDNS is not active for this domain."}
			case "GET /api/freedns/dns/":
				return 200, []pidginhost.FreeDNSDomain{{Domain: "example.com", Active: tc.zoneActive, Source: "internal"}}
			}
			return 500, map[string]any{"detail": "unexpected call " + call}
		})
		r := &dnsRecordResource{client: client}
		s := testSchema(r)
		state := dnsRecordModel{ID: types.StringValue("x"), Zone: types.StringValue("example.com"), Source: types.StringValue("internal"),
			Name: types.StringValue("www"), Type: types.StringValue("A"), TTL: types.Int64Value(14400), Address: types.StringValue("192.0.2.10")}
		resp := resource.ReadResponse{State: testState(t, s, state)}
		r.Read(context.Background(), resource.ReadRequest{State: testState(t, s, state)}, &resp)
		gone := resp.State.Raw.IsNull()
		if gone != tc.wantGone || resp.Diagnostics.HasError() == tc.wantGone {
			t.Fatalf("zone active %v: removed %v, diagnostics %v", tc.zoneActive, gone, resp.Diagnostics)
		}
	}
}

func TestDNSRecordDeleteAfterZoneDeactivation(t *testing.T) {
	for _, active := range []bool{false, true} {
		client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
			switch call {
			case "GET /api/freedns/dns/records/?domain=example.com&source=internal":
				return 400, map[string]any{"message": "FreeDNS is not active for this domain."}
			case "GET /api/freedns/dns/":
				return 200, []pidginhost.FreeDNSDomain{{Domain: "example.com", Active: active, Source: "internal"}}
			}
			return 500, nil
		})
		r := &dnsRecordResource{client: client}
		state := dnsRecordModel{Zone: types.StringValue("example.com"), Source: types.StringValue("internal"),
			Name: types.StringValue("www"), Type: types.StringValue("A"), Address: types.StringValue("192.0.2.10")}
		resp := resource.DeleteResponse{}
		r.Delete(t.Context(), resource.DeleteRequest{State: testState(t, testSchema(r), state)}, &resp)
		if resp.Diagnostics.HasError() != active {
			t.Fatalf("zone active %v: %v, calls %v", active, resp.Diagnostics, calls())
		}
		for _, call := range calls() {
			if !strings.HasPrefix(call, "GET ") {
				t.Fatalf("unexpected write %s", call)
			}
		}
	}
}
