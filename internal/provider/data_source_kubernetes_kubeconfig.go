package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &kubernetesKubeconfigDataSource{}

func NewKubernetesKubeconfigDataSource() datasource.DataSource {
	return &kubernetesKubeconfigDataSource{}
}

type kubernetesKubeconfigDataSource struct{ client *pidginhost.APIClient }

type kubernetesKubeconfigModel struct {
	ClusterID  types.String `tfsdk:"cluster_id"`
	Kubeconfig types.String `tfsdk:"kubeconfig"`
	ValidUntil types.String `tfsdk:"valid_until"`
}

func (d *kubernetesKubeconfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_kubeconfig"
}

func (d *kubernetesKubeconfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The current kubeconfig of a Kubernetes cluster. It holds cluster admin credentials, which " +
			"Terraform keeps in its state. With Terraform 1.10 or later, use the pidginhost_kubernetes_kubeconfig " +
			"ephemeral resource instead, which keeps them out of the state.",
		Attributes: map[string]schema.Attribute{
			"cluster_id": schema.StringAttribute{
				Required:    true,
				Description: "ID of the Kubernetes cluster.",
				Validators:  []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]+$`), "must be a numeric ID")},
			},
			"kubeconfig": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "Kubeconfig file with cluster admin credentials.",
			},
			"valid_until": schema.StringAttribute{
				Computed:    true,
				Description: "Time the kubeconfig expires.",
			},
		},
	}
}

func (d *kubernetesKubeconfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *kubernetesKubeconfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config kubernetesKubeconfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(fetchKubeconfig(ctx, d.client, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, config)...)
}

// fetchKubeconfig fills in the kubeconfig of cluster m.ClusterID and the time
// it expires.
func fetchKubeconfig(ctx context.Context, client *pidginhost.APIClient, m *kubernetesKubeconfigModel) (diags diag.Diagnostics) {
	id := m.ClusterID.ValueString()
	cluster, httpResp, err := client.KubernetesAPI.KubernetesClustersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		diags.AddError("Kubernetes cluster not found", fmt.Sprintf("Cluster %s does not exist.", id))
		return diags
	}
	if err != nil {
		diags.Append(apiError("Reading Kubernetes cluster", err))
		return diags
	}
	kubeconfig, httpResp, err := client.KubernetesAPI.KubernetesClustersKubeconfigRetrieve(ctx, id).Execute()
	if err != nil {
		diags.Append(secretRouteError("Reading kubeconfig for cluster "+id, httpResp, err))
		return diags
	}
	m.Kubeconfig = types.StringValue(kubeconfig)
	m.ValidUntil = types.StringPointerValue(cluster.KubeconfigValidUntil.Get())
	return diags
}
