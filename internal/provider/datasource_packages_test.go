package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestPackagesDataSourceFiltersByGeneration(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/server-packages/?generation=gen2&page=1": `{"count": 2, "next": "more", "results": [
			{"id": 4, "slug": "cloudv-2", "name": "Cloud V2", "cpus": 2, "memory": 4, "disk_size": 50, "traffic": 2, "available_generations": ["gen1", "gen2"]}]}`,
		"/api/cloud/server-packages/?generation=gen2&page=2": `{"count": 2, "next": null, "results": [
			{"id": 5, "slug": "cloudv-4", "name": "Cloud V4", "cpus": 4, "memory": 8, "disk_size": 100, "traffic": 4, "available_generations": ["gen2"]}]}`,
	})
	state, diags := readDataSource(t, NewPackagesDataSource(), client, map[string]tftypes.Value{
		"generation": tftypes.NewValue(tftypes.String, "gen2"),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	var packages []packageModel
	if diags := state.GetAttribute(t.Context(), path.Root("packages"), &packages); diags.HasError() {
		t.Fatal(diags)
	}
	if len(packages) != 2 || packages[0].ID != "4" || packages[0].Memory != 4 || packages[1].DiskSize != 100 ||
		len(packages[0].AvailableGenerations) != 2 {
		t.Fatalf("got %+v", packages)
	}
}

func TestAccPackagesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `
data "pidginhost_generations" "all" {}

data "pidginhost_packages" "all" {}

data "pidginhost_packages" "first_generation" {
  generation = data.pidginhost_generations.all.generations[0].slug
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_generations.all", tfjsonpath.New("generations").AtSliceIndex(0).AtMapKey("slug"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.pidginhost_packages.all", tfjsonpath.New("packages").AtSliceIndex(0).AtMapKey("cpus"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.pidginhost_packages.first_generation", tfjsonpath.New("packages"), knownvalue.NotNull()),
			},
		}},
	})
}
