package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccKubernetesKubeconfigDataSource(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "pidginhost_kubernetes_kubeconfig" "test" {
  cluster_id = %q
}
`, clusterID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.pidginhost_kubernetes_kubeconfig.test", tfjsonpath.New("kubeconfig"), knownvalue.NotNull()),
				},
			},
		},
	})
}

// readKubeconfig runs the data source against handler for cluster 7.
func readKubeconfig(t *testing.T, handler http.Handler) datasource.ReadResponse {
	t.Helper()
	ctx := context.Background()
	d := &kubernetesKubeconfigDataSource{client: testKubernetesAPI(t, handler)}
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	objectType := schemaResp.Schema.Type().TerraformType(ctx)
	config := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, nil)}
	if diags := config.Set(ctx, kubernetesKubeconfigModel{ClusterID: types.StringValue("7")}); diags.HasError() {
		t.Fatal(diags)
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, nil)}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: config.Raw}}, &resp)
	return resp
}

func kubeconfigAPI(status int, body string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, strings.Replace(clusterJSON("active", "1.35.7", true, false),
			`"kubeconfig_valid_until": null`, `"kubeconfig_valid_until": "2027-01-01T00:00:00Z"`, 1))
	})
	mux.HandleFunc("GET "+clusterPath+"kubeconfig/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func TestKubeconfigDataSourceReadsTheFile(t *testing.T) {
	resp := readKubeconfig(t, kubeconfigAPI(http.StatusOK, "apiVersion: v1\nkind: Config\n"))
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got kubernetesKubeconfigModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if got.Kubeconfig.ValueString() != "apiVersion: v1\nkind: Config\n" || got.ValidUntil.ValueString() != "2027-01-01T00:00:00Z" {
		t.Fatalf("got %+v %v", got, resp.Diagnostics)
	}
}

func TestKubeconfigDataSourceErrorsLeaveTheBodyOut(t *testing.T) {
	resp := readKubeconfig(t, kubeconfigAPI(http.StatusForbidden, "client-key-data: c2VjcmV0"))
	if !resp.Diagnostics.HasError() {
		t.Fatal("no error")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Detail(), "c2VjcmV0") {
			t.Fatalf("the response body leaked into %q", d.Detail())
		}
	}
}
