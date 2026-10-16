package provider

import (
	"context"
	"fmt"

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
	_ resource.ResourceWithConfigure   = &dnsZoneResource{}
	_ resource.ResourceWithImportState = &dnsZoneResource{}
)

func NewDNSZoneResource() resource.Resource { return &dnsZoneResource{} }

type dnsZoneResource struct{ client *pidginhost.APIClient }

type dnsZoneModel struct {
	ID     types.String `tfsdk:"id"`
	Domain types.String `tfsdk:"domain"`
	Source types.String `tfsdk:"source"`
	IP     types.String `tfsdk:"ip"`
}

func (r *dnsZoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (r *dnsZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "FreeDNS hosting for a domain: the zone pidginhost_dns_record adds records to. Activating the " +
			"zone of an internal domain points the domain's name servers at PidginHost. Destroying the resource " +
			"deactivates FreeDNS: the zone is deleted with all its records and an internal domain gets back the name " +
			"servers it had before. Do not manage the name servers of the same domain with " +
			"pidginhost_domain_nameservers.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "<domain>/<source>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain": schema.StringAttribute{
				Required:      true,
				Description:   "Domain name, such as example.com. Changing it replaces the zone.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source": dnsSourceAttribute(),
			"ip": schema.StringAttribute{
				Required: true,
				Description: "IPv4 address of the A record the API adds for the domain when it activates the zone. " +
					"It is only used then: changing it later changes nothing in the zone. To manage that A record " +
					"with pidginhost_dns_record, import it.",
			},
		},
	}
}

func (r *dnsZoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *dnsZoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, source := fqdn(plan.Domain.ValueString()), plan.Source.ValueString()
	body := pidginhost.NewActivateFreeDNSRequest(domain, pidginhost.SourceEnum(source), plan.IP.ValueString())
	if _, _, err := r.client.FreednsAPI.FreednsDnsActivateCreate(ctx).ActivateFreeDNSRequest(*body).Execute(); err != nil {
		resp.Diagnostics.Append(apiError("Activating FreeDNS for "+domain, err))
		return
	}
	plan.ID = types.StringValue(domain + "/" + source)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnsZoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zones, _, err := r.client.FreednsAPI.FreednsDnsList(ctx).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading FreeDNS zones", err))
		return
	}
	if findDNSZone(zones, state.Domain.ValueString(), state.Source.ValueString()) == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(fqdn(state.Domain.ValueString()) + "/" + state.Source.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only stores ip: the API uses it on activation alone.
func (r *dnsZoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnsZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnsZoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnsZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(state.Domain.ValueString())
	body := pidginhost.NewDeactivateFreeDNSRequest(domain, pidginhost.SourceEnum(state.Source.ValueString()))
	_, httpResp, err := r.client.FreednsAPI.FreednsDnsDeactivateCreate(ctx).DeactivateFreeDNSRequest(*body).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deactivating FreeDNS for "+domain, err))
	}
}

func (r *dnsZoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok || (parts[1] != "internal" && parts[1] != "external") {
		resp.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("Expected <domain>/<source>, such as example.com/internal; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("source"), parts[1])...)
}

// findDNSZone returns the active zone of domain from source, or nil.
func findDNSZone(zones []pidginhost.FreeDNSDomain, domain, source string) *pidginhost.FreeDNSDomain {
	for i, z := range zones {
		if z.Active && z.Source == source && fqdn(z.Domain) == fqdn(domain) {
			return &zones[i]
		}
	}
	return nil
}
