package provider

import (
	"testing"

	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccPrivateNetwork(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: `resource "pidginhost_private_network" "test" { address = "198.51.100.0/24" }`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_private_network.test", tfjsonpath.New("slug"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "pidginhost_private_network.test",
				ImportState:       true,
				ImportStateVerify: true,
				// Provisioning can finish between the apply and the import.
				ImportStateVerifyIgnore: []string{"provisioned"},
			},
			{
				Config: `
resource "pidginhost_private_network" "test" {
  address = "198.51.100.0/24"
  gateway = "198.51.100.254"
}
`,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_private_network.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_private_network.test", tfjsonpath.New("gateway"), knownvalue.StringExact("198.51.100.254")),
				},
			},
		},
	})
}
