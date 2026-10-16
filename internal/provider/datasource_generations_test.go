package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestGenerationsDataSource(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/generations/": `[
			{"slug": "gen2", "name": "Generation 2", "cpu_label": "AMD EPYC", "price_multiplier": "1.20", "is_default": true, "free_tier_eligible": false, "dedicated_cpu": false},
			{"slug": "gen1", "name": "Generation 1"}]`,
	})
	state, diags := readDataSource(t, NewGenerationsDataSource(), client, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var generations []generationModel
	if diags := state.GetAttribute(t.Context(), path.Root("generations"), &generations); diags.HasError() {
		t.Fatal(diags)
	}
	if len(generations) != 2 || *generations[0].PriceMultiplier != "1.20" || generations[1].IsDefault != nil {
		t.Fatalf("got %+v", generations)
	}
}

func TestAccGenerationsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `data "pidginhost_generations" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_generations.all", tfjsonpath.New("generations").AtSliceIndex(0).AtMapKey("slug"), knownvalue.NotNull()),
			},
		}},
	})
}
