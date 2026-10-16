package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccServerSnapshot snapshots a server it creates.
func TestAccServerSnapshot(t *testing.T) {
	name := testAccName("snap")
	servers := testAccServersConfig(t, name, 1)
	const res = "pidginhost_server_snapshot.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: servers + testAccServerSnapshotConfig(name, "first"),
				Check: func(s *terraform.State) error {
					server, ok := s.RootModule().Resources["pidginhost_server.acc0"]
					if !ok {
						return fmt.Errorf("pidginhost_server.acc0 not in state")
					}
					return resource.TestCheckResourceAttr(res, "id", server.Primary.ID+"/"+name)(s)
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("include_memory"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("created_at"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("is_current"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Deleting and taking the snapshot again under the same name exercises both waits.
				Config: servers + testAccServerSnapshotConfig(name, "second"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.StringExact("second")),
				},
			},
		},
	})
}

func testAccServerSnapshotConfig(name, description string) string {
	return fmt.Sprintf(`
resource "pidginhost_server_snapshot" "test" {
  server_id   = pidginhost_server.acc0.id
  name        = %q
  description = %q
}
`, name, description)
}
