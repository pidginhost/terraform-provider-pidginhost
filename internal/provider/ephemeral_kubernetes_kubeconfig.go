package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ ephemeral.EphemeralResourceWithConfigure = &kubernetesKubeconfigEphemeral{}

func NewKubernetesKubeconfigEphemeral() ephemeral.EphemeralResource {
	return &kubernetesKubeconfigEphemeral{}
}

type kubernetesKubeconfigEphemeral struct{ client *pidginhost.APIClient }

func (e *kubernetesKubeconfigEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_kubeconfig"
}

func (e *kubernetesKubeconfigEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The current kubeconfig of a Kubernetes cluster, read on every plan and apply and never saved " +
			"in the Terraform state. Use it to configure the kubernetes and helm providers. Needs Terraform 1.10 or later.",
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

func (e *kubernetesKubeconfigEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	e.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (e *kubernetesKubeconfigEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var config kubernetesKubeconfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(fetchKubeconfig(ctx, e.client, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, config)...)
}
