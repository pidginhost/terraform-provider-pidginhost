package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestCatalogDataSourceKeepsTheJSONAttributes(t *testing.T) {
	noPublicIP := `{"id": 42, "hostname": "private.example.com", "status": "active", "image": "ubuntu24", "package": "cloudv-2",
		"cpus": 2, "memory": 4, "disk_size": 50, "generation": "gen2", "destroy_protection": false, "ha_enabled": false,
		"custom_os": false, "rescue_mode": false, "boot_iso": null, "rescue_supported": true, "billing": null,
		"networks": {"public": {}, "private": []}}`
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/images/?page=1": `{"count": 1, "next": null, "results": [
			{"id": 12, "slug": "ubuntu24", "name": "Ubuntu 24.04", "family_name": "Ubuntu", "default_username": "ubuntu"}]}`,
		"/api/cloud/server-packages/?page=1": `{"count": 1, "next": null, "results": [
			{"id": 4, "slug": "cloudv-2", "name": "Cloud V2", "cpus": 2, "memory": 4, "disk_size": 50, "traffic": 2, "available_generations": ["gen2"]}]}`,
		"/api/account/profile": `{"first_name": "Ada", "last_name": "Example", "funds": "10.00", "phone": "+15555550100"}`,
		"/api/cloud/storage-products/?page=1": `{"count": 1, "next": null, "results": [
			{"id": 3, "slug": "fast-storage", "name": "Fast storage", "type": "storage", "unit": "GB", "price": "0.10", "min_size": 10, "max_size": 1000}]}`,
		"/api/cloud/volumes/":            `[]`,
		"/api/account/ssh-keys/?page=1":  `{"count": 0, "next": null, "results": []}`,
		"/api/cloud/firewall-rules-set/": `[{"id": 5, "name": "web", "status": "validated", "rules": [], "read_only": false}]`,
		"/api/cloud/ipv4/?page=1": `{"count": 1, "next": null, "results": [
			{"id": 9, "slug": "ip-9", "address": "192.0.2.10", "gateway": "192.0.2.1", "prefix": 24, "attached": true, "server": "web1.example.com", "server_id": 41}]}`,
		"/api/cloud/servers/?page=1": `{"count": 4, "next": "more", "results": [` +
			testServerJSON(41, "web1.example.com", "active") + `,` + noPublicIP + `]}`,
		"/api/cloud/servers/?page=2": `{"count": 4, "next": null, "results": [` +
			testServerJSON(43, "web3.example.com", "active") + `,` + testServerJSON(44, "old.example.com", "terminated") + `]}`,
		"/api/cloud/servers/41/public-interface/": `{"interface": "eth0", "ipv4": "192.0.2.10", "ipv6": "2001:db8::10",
			"fw_rules_set": "web", "fw_policy_in": "DROP", "fw_policy_out": "ACCEPT"}`,
		// 42 has no public address and 44 is terminated, so neither must be
		// asked; 43 answers 404 like a server whose public IP went away.
		"/api/cloud/servers/42/public-interface/": `{"interface": "eth0", "ipv4": "", "ipv6": ""}`,
		"/api/cloud/servers/44/public-interface/": `{"interface": "eth0", "ipv4": "192.0.2.44", "ipv6": ""}`,
	})
	state, diags := readDataSource(t, NewCatalogDataSource(), client, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var got catalogModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}

	var images, servers, publicInterfaces []map[string]any
	var profile map[string]any
	for raw, target := range map[string]any{
		got.ImagesJSON: &images, got.ServersJSON: &servers, got.PublicInterfacesJSON: &publicInterfaces, got.ProfileJSON: &profile,
	} {
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	if len(images) != 1 || images[0]["slug"] != "ubuntu24" || images[0]["default_username"] != "ubuntu" {
		t.Errorf("images_json: %s", got.ImagesJSON)
	}
	if len(servers) != 4 || servers[3]["hostname"] != "old.example.com" {
		t.Errorf("servers_json must hold every page: %s", got.ServersJSON)
	}
	if profile["first_name"] != "Ada" {
		t.Errorf("profile_json: %s", got.ProfileJSON)
	}
	if len(publicInterfaces) != 1 || publicInterfaces[0]["server_id"] != float64(41) ||
		publicInterfaces[0]["hostname"] != "web1.example.com" || publicInterfaces[0]["fw_rules_set"] != "web" {
		t.Errorf("public_interfaces_json: %s", got.PublicInterfacesJSON)
	}
	if got.VolumesJSON != "[]" || got.SSHKeysJSON != "[]" {
		t.Errorf("empty lists must encode as []: volumes %s, ssh keys %s", got.VolumesJSON, got.SSHKeysJSON)
	}
	for name, value := range map[string]string{
		"packages_json": got.PackagesJSON, "volume_products_json": got.VolumeProductsJSON,
		"firewalls_json": got.FirewallsJSON, "ips_json": got.IPsJSON,
	} {
		var items []map[string]any
		if err := json.Unmarshal([]byte(value), &items); err != nil || len(items) != 1 {
			t.Errorf("%s: %s", name, value)
		}
	}
}

func TestCatalogDataSourceIsDeprecatedAndHidesTheProfile(t *testing.T) {
	var resp datasource.SchemaResponse
	NewCatalogDataSource().Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	if resp.Schema.DeprecationMessage == "" {
		t.Error("pidginhost_catalog must be deprecated")
	}
	if !resp.Schema.Attributes["profile_json"].IsSensitive() {
		t.Error("profile_json holds personal data and must be sensitive")
	}
}

func TestAccCatalogDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `
data "pidginhost_catalog" "current" {}

output "image_slugs" {
  value = [for image in jsondecode(data.pidginhost_catalog.current.images_json) : image.slug]
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_catalog.current", tfjsonpath.New("servers_json"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.pidginhost_catalog.current", tfjsonpath.New("public_interfaces_json"), knownvalue.NotNull()),
				statecheck.ExpectKnownOutputValue("image_slugs", knownvalue.NotNull()),
			},
		}},
	})
}
