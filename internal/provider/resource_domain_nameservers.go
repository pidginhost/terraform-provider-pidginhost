package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.ResourceWithConfigure   = &domainNameserversResource{}
	_ resource.ResourceWithImportState = &domainNameserversResource{}
)

func NewDomainNameserversResource() resource.Resource { return &domainNameserversResource{} }

type domainNameserversResource struct{ client *pidginhost.APIClient }

type domainNameserversModel struct {
	ID          types.String   `tfsdk:"id"`
	Domain      types.String   `tfsdk:"domain"`
	Nameservers []types.String `tfsdk:"nameservers"`
}

func (r *domainNameserversResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_nameservers"
}

func (r *domainNameserversResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The name servers of a domain registered with PidginHost. The domain service must be active. " +
			"Destroying the resource only removes it from the Terraform state: the domain keeps its name servers. " +
			"Activating FreeDNS with pidginhost_dns_zone changes the name servers too: do not manage the same " +
			"domain with both.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Domain name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain": schema.StringAttribute{
				Required:      true,
				Description:   "Domain name, such as example.com.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"nameservers": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Name server host names, two to five. They are compared without case and trailing dot.",
				Validators: []validator.Set{
					setvalidator.SizeBetween(2, 5),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
		},
	}
}

func (r *domainNameserversResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *domainNameserversResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainNameserversModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(plan.Domain.ValueString())
	current, found, diags := r.nameservers(ctx, domain)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Domain not found", fmt.Sprintf("Domain %s is not registered on this account.", domain))
		return
	}
	// The registry is only asked to change what differs.
	if !sameNameservers(current, plan.names()) {
		if resp.Diagnostics.Append(r.set(ctx, domain, plan.names())...); resp.Diagnostics.HasError() {
			return
		}
	}
	plan.ID = types.StringValue(domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *domainNameserversResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainNameserversModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(state.Domain.ValueString())
	current, found, diags := r.nameservers(ctx, domain)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	// Keep the configured spelling while the set is the same.
	if !sameNameservers(current, state.names()) {
		state.Nameservers = make([]types.String, len(current))
		for i, ns := range current {
			state.Nameservers[i] = types.StringValue(ns)
		}
	}
	state.ID = types.StringValue(domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *domainNameserversResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainNameserversModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(plan.Domain.ValueString())
	if resp.Diagnostics.Append(r.set(ctx, domain, plan.names())...); resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete leaves the name servers as they are: a domain always has some, and
// the API cannot restore earlier ones.
func (r *domainNameserversResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *domainNameserversResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}

// nameservers returns the domain's name servers, and false when the account
// has no such domain. Errors show the status alone: the domain carries the
// registrant's personal data.
func (r *domainNameserversResource) nameservers(ctx context.Context, domain string) ([]string, bool, diag.Diagnostics) {
	d, httpResp, err := r.client.DomainAPI.DomainDomainRetrieve(ctx, domain).Execute()
	if isNotFound(httpResp, err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, diag.Diagnostics{domainError("Reading domain "+domain, httpResp, err)}
	}
	return splitNameservers(d.GetNameservers()), true, nil
}

func (r *domainNameserversResource) set(ctx context.Context, domain string, names []string) diag.Diagnostics {
	hosts := make([]string, len(names))
	for i, ns := range names {
		hosts[i] = fqdn(ns)
	}
	_, httpResp, err := r.client.DomainAPI.DomainDomainNameserversCreate(ctx, domain).
		NameserversUpdateRequest(*pidginhost.NewNameserversUpdateRequest(hosts)).Execute()
	if err != nil {
		return diag.Diagnostics{domainError("Setting the name servers of "+domain, httpResp, err)}
	}
	return nil
}

func (m *domainNameserversModel) names() []string {
	out := make([]string, len(m.Nameservers))
	for i, ns := range m.Nameservers {
		out[i] = ns.ValueString()
	}
	return out
}

// splitNameservers reads the comma separated name servers the API lists.
func splitNameservers(list string) []string {
	var out []string
	for _, ns := range strings.Split(list, ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// sameNameservers compares name servers as sets of host names, without case
// and trailing dot.
func sameNameservers(a, b []string) bool {
	normalize := func(names []string) []string {
		out := make([]string, len(names))
		for i, ns := range names {
			out[i] = fqdn(ns)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(normalize(a), normalize(b))
}
