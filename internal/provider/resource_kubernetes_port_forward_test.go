package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// internal_ip must be an address inside the test cluster; the documentation
// addresses below stand in for it.
func TestAccKubernetesPortForward(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccKubernetesPortForwardConfig(clusterID, "192.0.2.10"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_port_forward.test", tfjsonpath.New("port"), knownvalue.Int64Exact(30080)),
				},
			},
			{
				ResourceName:      "pidginhost_kubernetes_port_forward.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs := s.RootModule().Resources["pidginhost_kubernetes_port_forward.test"]
					return rs.Primary.Attributes["cluster_id"] + "/" + rs.Primary.ID, nil
				},
			},
			{
				Config: testAccKubernetesPortForwardConfig(clusterID, "192.0.2.11"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_port_forward.test", tfjsonpath.New("internal_ip"), knownvalue.StringExact("192.0.2.11")),
				},
			},
		},
	})
}

func testAccKubernetesPortForwardConfig(clusterID, internalIP string) string {
	return fmt.Sprintf(`
resource "pidginhost_kubernetes_port_forward" "test" {
  cluster_id  = %q
  port        = 30080
  protocol    = "tcp"
  internal_ip = %q
}
`, clusterID, internalIP)
}
