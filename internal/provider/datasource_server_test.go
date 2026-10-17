package provider

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func testServer(id int32, hostname string, status pidginhost.ResourceStatusEnum) pidginhost.Server {
	return pidginhost.Server{Id: id, Hostname: &hostname, Status: &status}
}

func TestFindServerByHostname(t *testing.T) {
	servers := []pidginhost.Server{
		testServer(1, "web1.example.com", pidginhost.RESOURCESTATUSENUM_ACTIVE),
		testServer(2, "web2.example.com", pidginhost.RESOURCESTATUSENUM_TERMINATED),
		testServer(3, "web2.example.com", pidginhost.RESOURCESTATUSENUM_PROVISIONING),
		testServer(4, "db.example.com", pidginhost.RESOURCESTATUSENUM_ACTIVE),
		testServer(5, "db.example.com", pidginhost.RESOURCESTATUSENUM_SUSPENDED),
		testServer(6, "old.example.com", pidginhost.RESOURCESTATUSENUM_CANCELLED),
		{Id: 7},
	}
	tests := []struct {
		hostname string
		want     int32
		err      string
	}{
		{hostname: "web1.example.com", want: 1},
		{hostname: "WEB1.example.com", want: 1},
		{hostname: "web2.example.com", want: 3},
		{hostname: "web1", err: `no server matches hostname "web1"`},
		{hostname: "old.example.com", err: `no server matches hostname "old.example.com"`},
		{hostname: "db.example.com", err: `2 servers match hostname "db.example.com" (ids 4, 5)`},
	}
	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			got, err := findServerByHostname(servers, tt.hostname)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("got %d, %v; want error %q", got, err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

// testServerJSON is a server as both the list and the detail endpoints
// return it.
func testServerJSON(id int, hostname, status string) string {
	return fmt.Sprintf(`{"id": %d, "hostname": %q, "status": %q, "project": "web", "image": "ubuntu24",
		"package": "cloudv-2", "cpus": 2, "memory": 4, "disk_size": 50, "generation": "gen2", "username": "ubuntu",
		"destroy_protection": true, "ha_enabled": false, "custom_os": false, "rescue_mode": false, "boot_iso": null,
		"rescue_supported": true, "machine": {}, "volumes": [], "floating_ips": [], "billing": null,
		"networks": {"public": {"interface": "eth0", "ipv4": "192.0.2.10", "ipv6": "2001:db8::10"},
			"private": [{"interface": "eth1", "address": "198.51.100.5", "network": "198.51.100.0/24"}]}}`,
		id, hostname, status)
}

func TestServerDataSourceLooksUpByHostname(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/servers/?page=1": `{"count": 2, "next": "more", "results": [` + testServerJSON(41, "web1.example.com", "active") + `]}`,
		"/api/cloud/servers/?page=2": `{"count": 2, "next": null, "results": [` + testServerJSON(42, "web2.example.com", "active") + `]}`,
		"/api/cloud/servers/42/":     testServerJSON(42, "web2.example.com", "active"),
	})
	state, diags := readDataSource(t, NewServerDataSource(), client, map[string]tftypes.Value{
		"hostname": tftypes.NewValue(tftypes.String, "web2.example.com"),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	var got serverDataModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	project := "web"
	want := serverDataModel{
		ID: "42", Hostname: "web2.example.com", Status: "active", Project: &project, Image: "ubuntu24",
		Package: "cloudv-2", Generation: "gen2", Cpus: 2, Memory: 4, DiskSize: 50, Username: "ubuntu",
		IPv4: "192.0.2.10", IPv6: "2001:db8::10", DestroyProtection: true,
		PrivateNetworks: []serverPrivateNetworkModel{{Network: "198.51.100.0/24", Address: "198.51.100.5", Interface: "eth1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestServerDataSourceLooksUpByID(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/servers/42/": testServerJSON(42, "web2.example.com", "active"),
		"/api/cloud/servers/43/": testServerJSON(43, "web3.example.com", "terminated"),
	})
	read := func(id string) (serverDataModel, string) {
		state, diags := readDataSource(t, NewServerDataSource(), client, map[string]tftypes.Value{
			"id": tftypes.NewValue(tftypes.String, id),
		})
		if diags.HasError() {
			return serverDataModel{}, diags[0].Detail()
		}
		var got serverDataModel
		if diags := state.Get(t.Context(), &got); diags.HasError() {
			t.Fatal(diags)
		}
		return got, ""
	}
	if got, err := read("42"); err != "" || got.Hostname != "web2.example.com" {
		t.Fatalf("got %+v, %s", got, err)
	}
	for id, want := range map[string]string{
		"43":  `no server matches id "43"`,
		"44":  `no server matches id "44"`,
		"web": "must be a number",
	} {
		if _, err := read(id); !strings.Contains(err, want) {
			t.Errorf("id %s: got error %q, want %q", id, err, want)
		}
	}
}

func TestAccServerDataSource(t *testing.T) {
	servers := testAccServersConfig(t, testAccName("ds"), 1)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: servers + `
data "pidginhost_server" "by_id" {
  id = pidginhost_server.acc0.id
}

data "pidginhost_server" "by_hostname" {
  hostname = data.pidginhost_server.by_id.hostname
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_server.by_id", tfjsonpath.New("status"), knownvalue.NotNull()),
				statecheck.CompareValuePairs("data.pidginhost_server.by_hostname", tfjsonpath.New("id"), "pidginhost_server.acc0", tfjsonpath.New("id"), compare.ValuesSame()),
			},
		}},
	})
}
