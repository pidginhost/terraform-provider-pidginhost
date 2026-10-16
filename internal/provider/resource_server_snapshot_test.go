package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccServerSnapshot snapshots an existing server: TF_ACC_SERVER_ID must
// name an active server on the test account, not booted from an ISO.
func TestAccServerSnapshot(t *testing.T) {
	serverID := os.Getenv("TF_ACC_SERVER_ID")
	name := testAccName("snap")
	const res = "pidginhost_server_snapshot.test"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			if serverID == "" {
				t.Fatal("acceptance tests need TF_ACC_SERVER_ID, the id of an active server to snapshot")
			}
		},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccServerSnapshotConfig(serverID, name, "first"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(serverID+"/"+name)),
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
				Config: testAccServerSnapshotConfig(serverID, name, "second"),
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

func testAccServerSnapshotConfig(serverID, name, description string) string {
	return fmt.Sprintf(`
resource "pidginhost_server_snapshot" "test" {
  server_id   = %q
  name        = %q
  description = %q
}
`, serverID, name, description)
}
