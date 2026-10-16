package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &kubernetesPortForwardResource{}
	_ resource.ResourceWithImportState = &kubernetesPortForwardResource{}
)

func NewKubernetesPortForwardResource() resource.Resource { return &kubernetesPortForwardResource{} }

type kubernetesPortForwardResource struct{ client *pidginhost.APIClient }

type kubernetesPortForwardModel struct {
	ID         types.String `tfsdk:"id"`
	ClusterID  types.String `tfsdk:"cluster_id"`
	Port       types.Int64  `tfsdk:"port"`
	Protocol   types.String `tfsdk:"protocol"`
	InternalIP types.String `tfsdk:"internal_ip"`
}

func (r *kubernetesPortForwardResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_port_forward"
}

func (r *kubernetesPortForwardResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Forwards a public port of a Kubernetes cluster to an address inside the cluster.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute(),
			"cluster_id": clusterIDAttribute(),
			"port": schema.Int64Attribute{
				Required:      true,
				Description:   "Public port to forward, up to 32767. Changing it replaces the port forward.",
				Validators:    []validator.Int64{int64validator.Between(0, 32767)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"protocol": schema.StringAttribute{
				Required:      true,
				Description:   "Protocol: tcp or udp. Changing it replaces the port forward.",
				Validators:    []validator.String{stringvalidator.OneOf("tcp", "udp")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"internal_ip": schema.StringAttribute{
				Required:    true,
				Description: "IPv4 address inside the cluster that receives the traffic.",
			},
		},
	}
}

func (r *kubernetesPortForwardResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *kubernetesPortForwardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesPortForwardModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := parseID(plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	body := pidginhost.K8sPortForwardRequest{
		InternalIp: plan.InternalIP.ValueString(),
		Port:       plan.Port.ValueInt64(),
		Protocol:   pidginhost.ProtocolEnum(plan.Protocol.ValueString()),
	}
	forward, _, err := r.client.KubernetesAPI.KubernetesClustersPortForwardsCreate(ctx, clusterID).K8sPortForwardRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating Kubernetes port forward", err))
		return
	}
	plan.fill(forward)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesPortForwardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesPortForwardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := parseID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	forward, httpResp, err := r.client.KubernetesAPI.KubernetesClustersPortForwardsRetrieve(ctx, clusterID, state.ID.ValueString()).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading Kubernetes port forward", err))
		return
	}
	state.fill(forward)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *kubernetesPortForwardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kubernetesPortForwardModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := parseID(plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	body := pidginhost.PatchedK8sPortForwardRequest{InternalIp: plan.InternalIP.ValueStringPointer()}
	forward, _, err := r.client.KubernetesAPI.KubernetesClustersPortForwardsPartialUpdate(ctx, clusterID, plan.ID.ValueString()).
		PatchedK8sPortForwardRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Updating Kubernetes port forward", err))
		return
	}
	plan.fill(forward)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesPortForwardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesPortForwardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := parseID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	httpResp, err := r.client.KubernetesAPI.KubernetesClustersPortForwardsDestroy(ctx, clusterID, state.ID.ValueString()).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting Kubernetes port forward", err))
	}
}

func (r *kubernetesPortForwardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <cluster_id>/<id>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func (m *kubernetesPortForwardModel) fill(forward *pidginhost.K8sPortForward) {
	m.ID = types.StringValue(strconv.Itoa(int(forward.Id)))
	m.Port = types.Int64Value(forward.Port)
	m.Protocol = types.StringValue(string(forward.Protocol))
	m.InternalIP = types.StringValue(forward.InternalIp)
}
