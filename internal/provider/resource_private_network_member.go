package provider

import (
	"context"
	"errors"
	"fmt"

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
	_ resource.ResourceWithConfigure   = &privateNetworkMemberResource{}
	_ resource.ResourceWithImportState = &privateNetworkMemberResource{}
)

func NewPrivateNetworkMemberResource() resource.Resource { return &privateNetworkMemberResource{} }

type privateNetworkMemberResource struct{ client *pidginhost.APIClient }

type privateNetworkMemberModel struct {
	ID        types.String `tfsdk:"id"`
	NetworkID types.String `tfsdk:"network_id"`
	ServerID  types.String `tfsdk:"server_id"`
	Address   types.String `tfsdk:"address"`
}

func (r *privateNetworkMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_private_network_member"
}

func (r *privateNetworkMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Membership of a cloud server in a private network. The server must be active to join. " +
			"Every attribute is fixed: changing one replaces the membership.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "<network_id>/<server_id>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"network_id": schema.StringAttribute{
				Required:      true,
				Description:   "ID of the private network.",
				Validators:    []validator.String{numericID},
				PlanModifiers: replace,
			},
			"server_id": schema.StringAttribute{
				Required:      true,
				Description:   "ID of the server.",
				Validators:    []validator.String{numericID},
				PlanModifiers: replace,
			},
			"address": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Private IPv4 address of the server on the network. It must be a free host address of " +
					"the network. The API picks one when left empty.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *privateNetworkMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *privateNetworkMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan privateNetworkMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	networkID, serverID, ok := plan.ids(&resp.Diagnostics)
	if !ok {
		return
	}
	network, server, err := r.lookup(ctx, networkID, serverID)
	if err == nil && network == nil {
		err = fmt.Errorf("private network %d not found", networkID)
	}
	if err == nil && server == nil {
		err = fmt.Errorf("server %d not found", serverID)
	}
	if err == nil && server.Status != pidginhost.RESOURCESTATUSENUM_ACTIVE {
		err = fmt.Errorf("server %d is %s; it must be active to join a private network", serverID, server.Status)
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Adding server to private network", err))
		return
	}
	body := pidginhost.NewPrivateNetworkAddHostRequest(plan.ServerID.ValueString())
	body.Address = optionalString(plan.Address)
	if _, _, err := r.client.CloudAPI.CloudPrivateNetworksAddServerCreate(ctx, networkID).PrivateNetworkAddHostRequest(*body).Execute(); err != nil {
		resp.Diagnostics.Append(apiError("Adding server to private network", err))
		return
	}
	plan.ID = types.StringValue(fmt.Sprintf("%d/%d", networkID, serverID))
	// Keep the membership in state before reading it back, so a failed read
	// taints it instead of leaving it outside Terraform.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), plan.NetworkID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), plan.ServerID)...)

	server, _, err = r.client.CloudAPI.CloudServersRetrieve(ctx, serverID).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server", err))
		return
	}
	iface := memberOf(server, network.Address)
	if iface == nil {
		resp.Diagnostics.AddError("Adding server to private network",
			fmt.Sprintf("Server %d does not list private network %s after joining it.", serverID, network.Address))
		return
	}
	plan.Address = types.StringValue(iface.Address)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *privateNetworkMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state privateNetworkMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	networkID, serverID, ok := state.ids(&resp.Diagnostics)
	if !ok {
		return
	}
	network, server, err := r.lookup(ctx, networkID, serverID)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading private network membership", err))
		return
	}
	var iface *pidginhost.ServerPrivateInterface
	if network != nil && server != nil {
		iface = memberOf(server, network.Address)
	}
	if iface == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(fmt.Sprintf("%d/%d", networkID, serverID))
	state.Address = types.StringValue(iface.Address)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update is never called: every attribute requires a replacement.
func (r *privateNetworkMemberResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "Private network memberships cannot change in place.")
}

func (r *privateNetworkMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state privateNetworkMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	networkID, serverID, ok := state.ids(&resp.Diagnostics)
	if !ok {
		return
	}
	network, server, err := r.lookup(ctx, networkID, serverID)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading private network membership", err))
		return
	}
	if network == nil || server == nil {
		return
	}
	iface := memberOf(server, network.Address)
	if iface == nil {
		return
	}
	// The private address names the member uniquely, unlike a hostname.
	body := pidginhost.NewPrivateNetworkRemoveHostRequest(iface.Address)
	_, httpResp, err := r.client.CloudAPI.CloudPrivateNetworksRemoveServerCreate(ctx, networkID).PrivateNetworkRemoveHostRequest(*body).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Removing server from private network", err))
	}
}

func (r *privateNetworkMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("Expected <network_id>/<server_id>, such as 12/345; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), parts[1])...)
}

// lookup fetches the network and the server, returning nil for either one
// the API no longer has.
func (r *privateNetworkMemberResource) lookup(ctx context.Context, networkID, serverID int32) (*pidginhost.PrivateNetwork, *pidginhost.ServerDetail, error) {
	network, httpResp, err := r.client.CloudAPI.CloudPrivateNetworksRetrieve(ctx, networkID).Execute()
	if isNotFound(httpResp, err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	server, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, serverID).Execute()
	if isNotFound(httpResp, err) {
		return network, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return network, server, nil
}

// memberOf returns the server's interface on the network with the given
// CIDR address, or nil when the server is not a member.
func memberOf(server *pidginhost.ServerDetail, network string) *pidginhost.ServerPrivateInterface {
	for i, iface := range server.Networks.Private {
		if iface.Network == network {
			return &server.Networks.Private[i]
		}
	}
	return nil
}

func (m *privateNetworkMemberModel) ids(diags interface{ AddError(string, string) }) (int32, int32, bool) {
	networkID, err := parseID(m.NetworkID.ValueString())
	serverID, serverErr := parseID(m.ServerID.ValueString())
	if err = errors.Join(err, serverErr); err != nil {
		diags.AddError("Invalid private network membership", err.Error())
		return 0, 0, false
	}
	return networkID, serverID, true
}
