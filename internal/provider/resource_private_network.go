package provider

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &privateNetworkResource{}
	_ resource.ResourceWithImportState = &privateNetworkResource{}
)

func NewPrivateNetworkResource() resource.Resource { return &privateNetworkResource{} }

type privateNetworkResource struct{ client *pidginhost.APIClient }

type privateNetworkModel struct {
	ID          types.String `tfsdk:"id"`
	Address     types.String `tfsdk:"address"`
	Gateway     types.String `tfsdk:"gateway"`
	Slug        types.String `tfsdk:"slug"`
	Provisioned types.Bool   `tfsdk:"provisioned"`
}

func (r *privateNetworkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_private_network"
}

func (r *privateNetworkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A private network that cloud servers join with pidginhost_private_network_member.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"address": schema.StringAttribute{
				Required:      true,
				Description:   "Network in CIDR notation, such as 198.51.100.0/24. Changing it replaces the network.",
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 32)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"gateway": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Gateway address on the network.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: computed,
			},
			"slug": schema.StringAttribute{
				Computed:      true,
				Description:   "Network in CIDR notation as the API names it.",
				PlanModifiers: computed,
			},
			"provisioned": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the API has finished setting up the network.",
			},
		},
	}
}

func (r *privateNetworkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *privateNetworkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan privateNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := pidginhost.NewPrivateNetworkRequest(plan.Address.ValueString())
	if gateway := optionalString(plan.Gateway); gateway != nil {
		body.Gateway.Set(gateway)
	}
	network, _, err := r.client.CloudAPI.CloudPrivateNetworksCreate(ctx).PrivateNetworkRequest(*body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating private network", err))
		return
	}
	plan.fill(network)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *privateNetworkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state privateNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid private network id", err.Error())
		return
	}
	network, httpResp, err := r.client.CloudAPI.CloudPrivateNetworksRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading private network", err))
		return
	}
	state.fill(network)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *privateNetworkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan privateNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid private network id", err.Error())
		return
	}
	var body pidginhost.PatchedPrivateNetworkUpdateRequest
	body.Gateway.Set(optionalString(plan.Gateway))
	network, _, err := r.client.CloudAPI.CloudPrivateNetworksPartialUpdate(ctx, id).PatchedPrivateNetworkUpdateRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Updating private network", err))
		return
	}
	plan.fill(network)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *privateNetworkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state privateNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid private network id", err.Error())
		return
	}
	httpResp, err := r.client.CloudAPI.CloudPrivateNetworksDestroy(ctx, id).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting private network", err))
	}
}

func (r *privateNetworkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *privateNetworkModel) fill(n *pidginhost.PrivateNetwork) {
	m.ID = types.StringValue(strconv.Itoa(int(n.Id)))
	m.Address = types.StringValue(n.Address)
	m.Gateway = types.StringPointerValue(n.Gateway.Get())
	m.Slug = types.StringValue(n.Slug)
	m.Provisioned = types.BoolValue(n.Provisioned)
}
