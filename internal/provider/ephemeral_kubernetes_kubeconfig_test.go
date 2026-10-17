package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// echoProviders adds the echo provider, which copies an ephemeral value into
// the state of echo.test so a test can check it.
var echoProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"pidginhost": testAccProviders["pidginhost"],
	"echo":       echoprovider.NewProviderServer(),
}

func TestAccKubernetesKubeconfigEphemeral(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: echoProviders,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
ephemeral "pidginhost_kubernetes_kubeconfig" "test" {
  cluster_id = %q
}

locals {
  kubeconfig = yamldecode(ephemeral.pidginhost_kubernetes_kubeconfig.test.kubeconfig)
  cluster    = local.kubeconfig.clusters[0].cluster
  user       = local.kubeconfig.users[0].user
}

# The fields the Kubernetes providers guide passes to the kubernetes and helm
# providers.
provider "echo" {
  data = {
    server      = startswith(local.cluster.server, "https://")
    ca          = length(base64decode(local.cluster["certificate-authority-data"])) > 0
    credentials = can(base64decode(local.user["client-key-data"])) || can(local.user.token)
  }
}

resource "echo" "test" {}
`, clusterID),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data"), knownvalue.ObjectExact(map[string]knownvalue.Check{
					"server": knownvalue.Bool(true), "ca": knownvalue.Bool(true), "credentials": knownvalue.Bool(true),
				})),
			},
		}},
	})
}

func TestKubeconfigEphemeralReachesAProviderBlock(t *testing.T) {
	srv := httptest.NewServer(kubeconfigAPI(http.StatusOK, "apiVersion: v1\nkind: Config\n"))
	defer srv.Close()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: echoProviders,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "pidginhost" {
  token    = "test"
  base_url = %q
}

ephemeral "pidginhost_kubernetes_kubeconfig" "test" {
  cluster_id = "7"
}

provider "echo" {
  data = ephemeral.pidginhost_kubernetes_kubeconfig.test
}

resource "echo" "test" {}
`, srv.URL),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data").AtMapKey("kubeconfig"),
					knownvalue.StringExact("apiVersion: v1\nkind: Config\n")),
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data").AtMapKey("valid_until"),
					knownvalue.StringExact("2027-01-01T00:00:00Z")),
			},
		}},
	})
}

func TestKubeconfigEphemeralErrorsLeaveTheBodyOut(t *testing.T) {
	ctx := context.Background()
	e := &kubernetesKubeconfigEphemeral{client: testKubernetesAPI(t, kubeconfigAPI(http.StatusForbidden, "client-key-data: c2VjcmV0"))}
	var schemaResp ephemeral.SchemaResponse
	e.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
	objectType := schemaResp.Schema.Type().TerraformType(ctx)
	config := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
		"cluster_id":  tftypes.NewValue(tftypes.String, "7"),
		"kubeconfig":  tftypes.NewValue(tftypes.String, nil),
		"valid_until": tftypes.NewValue(tftypes.String, nil),
	})}
	resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, nil)}}
	e.Open(ctx, ephemeral.OpenRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("no error")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Detail(), "c2VjcmV0") {
			t.Fatalf("the response body leaked into %q", d.Detail())
		}
	}
	if !resp.Result.Raw.IsNull() {
		t.Fatal("a failed open returned a result")
	}
}
