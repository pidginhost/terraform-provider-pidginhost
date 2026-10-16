package provider

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &floatingIPResource{}
	_ resource.ResourceWithImportState = &floatingIPResource{}
)

func NewFloatingIPResource() resource.Resource { return &floatingIPResource{} }

type floatingIPResource struct{ client *pidginhost.APIClient }

type floatingIPModel struct {
	ID                  types.String `tfsdk:"id"`
	Version             types.Int64  `tfsdk:"version"`
	Label               types.String `tfsdk:"label"`
	ReverseDNS          types.String `tfsdk:"reverse_dns"`
	AuthorizedServerIDs []string     `tfsdk:"authorized_server_ids"`
	Address             types.String `tfsdk:"address"`
	Gateway             types.String `tfsdk:"gateway"`
	Prefix              types.Int64  `tfsdk:"prefix"`
}

func (r *floatingIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_floating_ip"
}

func (r *floatingIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A floating IPv4 or IPv6 address that the authorized servers can take over.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"version": schema.Int64Attribute{
				Required:      true,
				Description:   "IP version: 4 or 6. Changing it replaces the address.",
				Validators:    []validator.Int64{int64validator.OneOf(4, 6)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"label": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(""),
				Description:   "Label of the address. The API cannot rename it, so changing it replaces the address.",
				Validators:    []validator.String{stringvalidator.LengthAtMost(64)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"reverse_dns": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Reverse DNS (PTR) name of the address.",
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 200)},
				PlanModifiers: computed,
			},
			"authorized_server_ids": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "IDs of the servers allowed to use the address. The set is authoritative: servers " +
					"authorized outside it lose their authorization.",
				Validators: []validator.Set{setvalidator.ValueStringsAre(numericID)},
			},
			"address": schema.StringAttribute{
				Computed:      true,
				Description:   "The IP address.",
				PlanModifiers: computed,
			},
			"gateway": schema.StringAttribute{
				Computed:      true,
				Description:   "Gateway of the address.",
				PlanModifiers: computed,
			},
			"prefix": schema.Int64Attribute{
				Computed:      true,
				Description:   "Prefix length of the address network.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *floatingIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *floatingIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan floatingIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	version := plan.Version.ValueInt64()
	fip, err := r.create(ctx, version, plan.Label.ValueString())
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating floating IP", err))
		return
	}
	// Keep the billed address in state before the follow-up calls can fail.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.Itoa(int(fip.Id)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), version)...)
	if !plan.ReverseDNS.IsUnknown() && !plan.ReverseDNS.IsNull() {
		if err := r.setReverseDNS(ctx, version, fip.Id, plan.ReverseDNS.ValueString()); err != nil {
			resp.Diagnostics.Append(apiError("Setting reverse DNS", err))
			return
		}
	}
	if err := r.authorize(ctx, version, fip.Id, nil, plan.AuthorizedServerIDs); err != nil {
		resp.Diagnostics.Append(apiError("Authorizing servers", err))
		return
	}
	r.readInto(ctx, &plan, fip.Id, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *floatingIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state floatingIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid floating IP id", err.Error())
		return
	}
	fip, httpResp, err := r.get(ctx, state.Version.ValueInt64(), id)
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = r.fill(ctx, &state, fip)
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading floating IP", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *floatingIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state floatingIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid floating IP id", err.Error())
		return
	}
	version := state.Version.ValueInt64()
	if !plan.ReverseDNS.IsUnknown() && !plan.ReverseDNS.IsNull() && !plan.ReverseDNS.Equal(state.ReverseDNS) {
		if err := r.setReverseDNS(ctx, version, id, plan.ReverseDNS.ValueString()); err != nil {
			resp.Diagnostics.Append(apiError("Setting reverse DNS", err))
			return
		}
	}
	have, err := r.authorized(ctx, version, id)
	if err == nil {
		err = r.authorize(ctx, version, id, have, plan.AuthorizedServerIDs)
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Authorizing servers", err))
		return
	}
	r.readInto(ctx, &plan, id, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *floatingIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state floatingIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid floating IP id", err.Error())
		return
	}
	var httpResp *http.Response
	if state.Version.ValueInt64() == 6 {
		httpResp, err = r.client.CloudAPI.CloudFloatingIpv6Destroy(ctx, id).Execute()
	} else {
		httpResp, err = r.client.CloudAPI.CloudFloatingIpv4Destroy(ctx, id).Execute()
	}
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting floating IP", err))
	}
}

func (r *floatingIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importIPVersionAndID(ctx, req, resp)
}

// authorize authorizes the servers in want that are not in have and
// removes the authorization of those in have that are not in want.
func (r *floatingIPResource) authorize(ctx context.Context, version int64, id int32, have, want []string) error {
	add, remove := diffStrings(have, want)
	for _, server := range remove {
		if err := r.setAuthorized(ctx, version, id, server, false); err != nil {
			return err
		}
	}
	for _, server := range add {
		if err := r.setAuthorized(ctx, version, id, server, true); err != nil {
			return err
		}
	}
	return nil
}

func (r *floatingIPResource) setAuthorized(ctx context.Context, version int64, id int32, server string, allowed bool) error {
	serverID, err := parseID(server)
	if err != nil {
		return err
	}
	body := *pidginhost.NewFloatingIPAuthorizeRequest(serverID)
	switch {
	case version == 6 && allowed:
		_, _, err = r.client.CloudAPI.CloudFloatingIpv6AuthorizeCreate(ctx, id).FloatingIPAuthorizeRequest(body).Execute()
	case version == 6:
		_, _, err = r.client.CloudAPI.CloudFloatingIpv6UnauthorizeCreate(ctx, id).FloatingIPAuthorizeRequest(body).Execute()
	case allowed:
		_, _, err = r.client.CloudAPI.CloudFloatingIpv4AuthorizeCreate(ctx, id).FloatingIPAuthorizeRequest(body).Execute()
	default:
		_, _, err = r.client.CloudAPI.CloudFloatingIpv4UnauthorizeCreate(ctx, id).FloatingIPAuthorizeRequest(body).Execute()
	}
	return err
}

// diffStrings returns the values of want missing from have, and the values
// of have missing from want.
func diffStrings(have, want []string) (add, remove []string) {
	for _, w := range want {
		if !slices.Contains(have, w) {
			add = append(add, w)
		}
	}
	for _, h := range have {
		if !slices.Contains(want, h) {
			remove = append(remove, h)
		}
	}
	return add, remove
}

// authorized returns the sorted ids of the servers allowed to use the address.
func (r *floatingIPResource) authorized(ctx context.Context, version int64, id int32) ([]string, error) {
	rows, err := fetchAll(func(page int32) ([]pidginhost.FloatingIPAuthorization, bool, error) {
		var list *pidginhost.PaginatedFloatingIPAuthorizationList
		var err error
		if version == 6 {
			list, _, err = r.client.CloudAPI.CloudFloatingIpv6AuthorizationsList(ctx, id).Page(page).Execute()
		} else {
			list, _, err = r.client.CloudAPI.CloudFloatingIpv4AuthorizationsList(ctx, id).Page(page).Execute()
		}
		if err != nil {
			return nil, false, err
		}
		return list.Results, list.GetNext() != "", nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, strconv.Itoa(int(row.ServerId)))
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (r *floatingIPResource) readInto(ctx context.Context, m *floatingIPModel, id int32, diags *diag.Diagnostics) {
	fip, _, err := r.get(ctx, m.Version.ValueInt64(), id)
	if err == nil {
		err = r.fill(ctx, m, fip)
	}
	if err != nil {
		diags.Append(apiError("Reading floating IP", err))
	}
}

// fill copies fip and its authorized servers, served separately, into m.
func (r *floatingIPResource) fill(ctx context.Context, m *floatingIPModel, fip *pidginhost.FloatingIPv4) error {
	authorized, err := r.authorized(ctx, m.Version.ValueInt64(), fip.Id)
	if err != nil {
		return err
	}
	m.ID = types.StringValue(strconv.Itoa(int(fip.Id)))
	m.Label = types.StringValue(fip.GetLabel())
	m.ReverseDNS = types.StringValue(fip.ReverseDns)
	m.Address = types.StringValue(fip.Address)
	m.Gateway = types.StringValue(fip.Gateway)
	m.Prefix = types.Int64Value(fip.Prefix)
	// An unset set stays unset while no server is authorized.
	if len(authorized) > 0 || m.AuthorizedServerIDs != nil {
		m.AuthorizedServerIDs = authorized
	}
	return nil
}

// The API has separate IPv4 and IPv6 endpoints with identical models; these
// helpers pick one and return the IPv4 model.

func (r *floatingIPResource) get(ctx context.Context, version int64, id int32) (*pidginhost.FloatingIPv4, *http.Response, error) {
	if version == 6 {
		fip, httpResp, err := r.client.CloudAPI.CloudFloatingIpv6Retrieve(ctx, id).Execute()
		if err != nil {
			return nil, httpResp, err
		}
		v4 := pidginhost.FloatingIPv4(*fip)
		return &v4, httpResp, nil
	}
	return r.client.CloudAPI.CloudFloatingIpv4Retrieve(ctx, id).Execute()
}

func (r *floatingIPResource) create(ctx context.Context, version int64, label string) (*pidginhost.FloatingIPv4, error) {
	if version == 6 {
		body := pidginhost.FloatingIPv6CreateRequest{Label: &label}
		fip, _, err := r.client.CloudAPI.CloudFloatingIpv6Create(ctx).FloatingIPv6CreateRequest(body).Execute()
		if err != nil {
			return nil, err
		}
		v4 := pidginhost.FloatingIPv4(*fip)
		return &v4, nil
	}
	body := pidginhost.FloatingIPv4CreateRequest{Label: &label}
	fip, _, err := r.client.CloudAPI.CloudFloatingIpv4Create(ctx).FloatingIPv4CreateRequest(body).Execute()
	return fip, err
}

func (r *floatingIPResource) setReverseDNS(ctx context.Context, version int64, id int32, name string) error {
	body := *pidginhost.NewReverseDNSRequest(name)
	var err error
	if version == 6 {
		_, _, err = r.client.CloudAPI.CloudFloatingIpv6RdnsCreate(ctx, id).ReverseDNSRequest(body).Execute()
	} else {
		_, _, err = r.client.CloudAPI.CloudFloatingIpv4RdnsCreate(ctx, id).ReverseDNSRequest(body).Execute()
	}
	return err
}
