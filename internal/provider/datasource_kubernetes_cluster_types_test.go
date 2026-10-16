package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestKubernetesClusterTypesDataSourceWalksEveryPage(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/kubernetes/cluster-types/?page=1": `{"count": 2, "next": "more", "results": [
			{"type": "dev", "worker_nodes_count_min": 1, "worker_nodes_count_max": 3,
			 "worker_node_packages": [{"id": 7, "slug": "k8s-small", "name": "Small"}]}]}`,
		"/api/kubernetes/cluster-types/?page=2": `{"count": 2, "next": null, "results": [
			{"type": "prod", "worker_node_packages": []}]}`,
	})
	state, diags := readDataSource(t, NewKubernetesClusterTypesDataSource(), client, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var types []clusterTypeModel
	if diags := state.GetAttribute(t.Context(), path.Root("cluster_types"), &types); diags.HasError() {
		t.Fatal(diags)
	}
	if len(types) != 2 || types[0].Type != "dev" || *types[0].WorkerNodesCountMax != 3 ||
		types[0].WorkerNodePackages[0].ID != "7" || types[1].WorkerNodesCountMin != nil || len(types[1].WorkerNodePackages) != 0 {
		t.Fatalf("got %+v", types)
	}
}

func TestAccKubernetesClusterTypesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `data "pidginhost_kubernetes_cluster_types" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_kubernetes_cluster_types.all", tfjsonpath.New("cluster_types").AtSliceIndex(0).AtMapKey("type"), knownvalue.NotNull()),
			},
		}},
	})
}
