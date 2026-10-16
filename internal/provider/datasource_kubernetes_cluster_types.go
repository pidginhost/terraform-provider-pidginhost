package provider

import (
	"context"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &kubernetesClusterTypesDataSource{}

func NewKubernetesClusterTypesDataSource() datasource.DataSource {
	return &kubernetesClusterTypesDataSource{}
}

type kubernetesClusterTypesDataSource struct{ dataSourceBase }

type clusterTypeModel struct {
	Type                string                `tfsdk:"type"`
	WorkerNodesCountMin *int64                `tfsdk:"worker_nodes_count_min"`
	WorkerNodesCountMax *int64                `tfsdk:"worker_nodes_count_max"`
	WorkerNodePackages  []clusterPackageModel `tfsdk:"worker_node_packages"`
}

type clusterPackageModel struct {
	ID   string `tfsdk:"id"`
	Slug string `tfsdk:"slug"`
	Name string `tfsdk:"name"`
}

func (d *kubernetesClusterTypesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster_types"
}

func (d *kubernetesClusterTypesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Kubernetes cluster types, with the worker packages and node counts each allows.",
		Attributes: map[string]schema.Attribute{
			"cluster_types": computedList("Every cluster type.", map[string]schema.Attribute{
				"type":                   computedString("Cluster type, such as dev or prod, accepted as a cluster's cluster_type."),
				"worker_nodes_count_min": computedInt("Fewest worker nodes a cluster of this type can have. Null when unbounded."),
				"worker_nodes_count_max": computedInt("Most worker nodes a cluster of this type can have. Null when unbounded."),
				"worker_node_packages": computedList("Packages worker nodes of this type can use.", map[string]schema.Attribute{
					"id":   computedString("Package id."),
					"slug": computedString("Short name of the package."),
					"name": computedString("Display name."),
				}),
			}),
		},
	}
}

func (d *kubernetesClusterTypesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	types, err := listAll(func(page int32) (*pidginhost.PaginatedClusterTypeList, *http.Response, error) {
		return d.client.KubernetesAPI.KubernetesClusterTypesList(ctx).Page(page).Execute()
	})
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing Kubernetes cluster types", err))
		return
	}
	models := make([]clusterTypeModel, len(types))
	for i, t := range types {
		packages := make([]clusterPackageModel, len(t.WorkerNodePackages))
		for j, p := range t.WorkerNodePackages {
			packages[j] = clusterPackageModel{ID: strconv.Itoa(int(p.Id)), Slug: p.Slug, Name: p.Name}
		}
		models[i] = clusterTypeModel{
			Type:                string(t.Type),
			WorkerNodesCountMin: t.WorkerNodesCountMin,
			WorkerNodesCountMax: t.WorkerNodesCountMax,
			WorkerNodePackages:  packages,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		ClusterTypes []clusterTypeModel `tfsdk:"cluster_types"`
	}{models})...)
}
