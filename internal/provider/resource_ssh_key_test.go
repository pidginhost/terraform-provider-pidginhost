package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccSSHKey(t *testing.T) {
	key := testAccSSHPublicKey(t)
	alias := testAccName("key")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccSSHKeyConfig(alias, key+"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_ssh_key.test", tfjsonpath.New("alias"), knownvalue.StringExact(alias)),
					statecheck.ExpectKnownValue("pidginhost_ssh_key.test", tfjsonpath.New("fingerprint"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:            "pidginhost_ssh_key.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"key"},
			},
			{
				Config: testAccSSHKeyConfig(alias+"-renamed", key+"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_ssh_key.test", tfjsonpath.New("alias"), knownvalue.StringExact(alias+"-renamed")),
				},
			},
		},
	})
}

func testAccSSHKeyConfig(alias, key string) string {
	return fmt.Sprintf(`
resource "pidginhost_ssh_key" "test" {
  alias = %q
  key   = %q
}
`, alias, key)
}
