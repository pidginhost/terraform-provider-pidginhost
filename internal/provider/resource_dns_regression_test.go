package provider

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestDNSRecordImportPreservesRootTarget(t *testing.T) {
	for _, rtype := range []string{"MX", "SRV"} {
		t.Run(rtype, func(t *testing.T) {
			record := pidginhost.DNSRecord{Line: 2, Name: "example.com.", Ttl: 3600, Type: rtype}
			field := "exchange"
			if rtype == "MX" {
				record.Exchange, record.Preference = pidginhost.PtrString("."), pidginhost.PtrInt32(0)
			} else {
				field = "target"
				record.Target = pidginhost.PtrString(".")
				record.Priority, record.Weight, record.Port = pidginhost.PtrInt32(0), pidginhost.PtrInt32(0), pidginhost.PtrInt32(0)
			}
			zone := &fakeZoneRecords{records: []pidginhost.DNSRecord{{Line: 1, Name: "example.com.", Type: "SOA"}, record}}
			var postedTarget any
			client, _ := fakeAPI(t, func(call string, body map[string]any) (int, any) {
				if strings.HasPrefix(call, "POST ") {
					postedTarget = body[field]
				}
				return zone.handle(call, body)
			})
			r := &dnsRecordResource{client: client}
			s := testSchema(r)
			imported := resource.ImportStateResponse{State: testState(t, s, nil)}
			r.ImportState(t.Context(), resource.ImportStateRequest{ID: "example.com/internal/@/" + rtype}, &imported)
			var plan dnsRecordModel
			imported.Diagnostics.Append(imported.State.Get(t.Context(), &plan)...)
			if imported.Diagnostics.HasError() {
				t.Fatal(imported.Diagnostics)
			}
			if got := plan.values()[field]; got != "." {
				t.Errorf("imported %s = %q, want root target", field, got)
			}
			plan.TTL = types.Int64Value(7200)
			updated := resource.UpdateResponse{State: imported.State}
			r.Update(t.Context(), resource.UpdateRequest{State: imported.State, Plan: testPlan(t, s, plan)}, &updated)
			if updated.Diagnostics.HasError() || postedTarget != "." {
				t.Fatalf("TTL edit changed root target: posted %v, diagnostics %v", postedTarget, updated.Diagnostics)
			}
		})
	}
}

func TestDNSRecordTTLVerifiesStoredRecord(t *testing.T) {
	for _, tc := range []struct {
		name        string
		writeStatus int
		storedTTL   int32
		readStatus  int
		missing     bool
		wantError   bool
	}{
		{"accepted edit payload", 200, 7200, 200, false, false},
		{"write refused", 400, 7200, 200, false, true},
		{"TTL unchanged", 200, 3600, 200, false, true},
		{"read failed", 200, 7200, 503, false, true},
		{"record missing", 200, 7200, 200, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			written, reads := false, 0
			record := pidginhost.DNSRecord{Line: 2, Name: "www.example.com.", Type: "A", Ttl: 3600, Address: pidginhost.PtrString("192.0.2.10")}
			client, _ := fakeAPI(t, func(call string, body map[string]any) (int, any) {
				switch call {
				case "GET /api/freedns/dns/records/?domain=example.com&source=internal":
					reads++
					if written {
						if tc.readStatus != 200 {
							return tc.readStatus, nil
						}
						if tc.missing {
							return 200, []pidginhost.DNSRecord{}
						}
						if tc.writeStatus == 200 {
							record.Ttl = tc.storedTTL
						}
					}
					return 200, []pidginhost.DNSRecord{record}
				case "POST /api/freedns/dns/add-record/?domain=example.com&source=internal":
					written = true
					if tc.writeStatus != 200 {
						return tc.writeStatus, map[string]any{"message": "edit refused"}
					}
					// The edit endpoint echoes the zone backend payload. Its numbers
					// are strings, unlike the typed records returned by the list.
					body["Line"], body["ttl"] = body["line"], fmt.Sprint(body["ttl"])
					return 200, map[string]any{"message": "Record saved successfully.", "record": body}
				}
				return 500, nil
			})
			client.GetConfig().HTTPClient.Transport.(*retryTransport).wait = time.Millisecond
			r := &dnsRecordResource{client: client}
			s := testSchema(r)
			state := dnsRecordModel{Zone: types.StringValue("example.com"), Source: types.StringValue("internal"),
				Name: types.StringValue("www"), Type: types.StringValue("A"), TTL: types.Int64Value(3600), Address: types.StringValue("192.0.2.10")}
			state.ID = state.id()
			plan := state
			plan.TTL = types.Int64Value(7200)
			prior := testState(t, s, state)
			resp := resource.UpdateResponse{State: prior}
			r.Update(t.Context(), resource.UpdateRequest{State: prior, Plan: testPlan(t, s, plan)}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics %v, want error %v", resp.Diagnostics, tc.wantError)
			}
			var got dnsRecordModel
			if diags := resp.State.Get(t.Context(), &got); diags.HasError() {
				t.Fatal(diags)
			}
			if tc.wantError {
				if !got.TTL.Equal(state.TTL) {
					t.Fatal("unverified TTL saved to state")
				}
			} else if reads != 2 || !got.TTL.Equal(plan.TTL) {
				t.Fatalf("expected verified TTL: reads %d, state TTL %v", reads, got.TTL)
			}
		})
	}
}

func TestDNSZoneDeleteAlreadyInactive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		active     bool
		listStatus int
		wantError  bool
	}{
		{"inactive", false, 200, false},
		{"active", true, 200, true},
		{"unknown", false, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
				switch call {
				case "POST /api/freedns/dns/deactivate/":
					return 400, map[string]any{"message": "Cannot deactivate FreeDNS."}
				case "GET /api/freedns/dns/":
					if tc.listStatus != 200 {
						return tc.listStatus, nil
					}
					return 200, []pidginhost.FreeDNSDomain{{Domain: "example.com", Source: "internal", Active: tc.active}}
				}
				return 500, nil
			})
			client.GetConfig().HTTPClient.Transport.(*retryTransport).wait = time.Millisecond
			r := &dnsZoneResource{client: client}
			state := dnsZoneModel{Domain: types.StringValue("example.com"), Source: types.StringValue("internal")}
			resp := resource.DeleteResponse{}
			r.Delete(t.Context(), resource.DeleteRequest{State: testState(t, testSchema(r), state)}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics %v, want error %v", resp.Diagnostics, tc.wantError)
			}
		})
	}
}
