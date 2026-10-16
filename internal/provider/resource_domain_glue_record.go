package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

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
	_ resource.ResourceWithConfigure   = &domainGlueRecordResource{}
	_ resource.ResourceWithImportState = &domainGlueRecordResource{}
)

func NewDomainGlueRecordResource() resource.Resource { return &domainGlueRecordResource{} }

type domainGlueRecordResource struct{ client *pidginhost.APIClient }

type domainGlueRecordModel struct {
	ID     types.String `tfsdk:"id"`
	Domain types.String `tfsdk:"domain"`
	Name   types.String `tfsdk:"name"`
	IP     types.String `tfsdk:"ip"`
	IP2    types.String `tfsdk:"ip2"`
}

func (r *domainGlueRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_glue_record"
}

func (r *domainGlueRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	notEmpty := stringvalidator.LengthAtLeast(1)
	resp.Schema = schema.Schema{
		Description: "A glue record of a domain registered with PidginHost: it registers <name>.<domain> at the " +
			"registry as a name server host with the given addresses, so domains can delegate to it. The domain " +
			"service must be active.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "<domain>/<name>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain": schema.StringAttribute{
				Required:      true,
				Description:   "Domain name, such as example.com.",
				Validators:    []validator.String{notEmpty},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Label of the name server host, such as ns1 for ns1.example.com. It is compared without case.",
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 63)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ip": schema.StringAttribute{
				Required:    true,
				Description: "IP address of the name server host.",
				Validators:  []validator.String{notEmpty},
			},
			"ip2": schema.StringAttribute{
				Optional:    true,
				Description: "Second IP address of the name server host.",
				Validators:  []validator.String{notEmpty},
			},
		},
	}
}

func (r *domainGlueRecordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *domainGlueRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainGlueRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, name := fqdn(plan.Domain.ValueString()), plan.Name.ValueString()
	// The API updates the record of the same name instead of refusing it.
	existing, diags := r.find(ctx, domain, name)
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError("Glue record already exists", fmt.Sprintf(
			"Domain %s already has the glue record %s. Import it with the id %s/%s.", domain, existing.Name, domain, existing.Name))
		return
	}
	if resp.Diagnostics.Append(r.upsert(ctx, domain, name, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(domain + "/" + strings.ToLower(name))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *domainGlueRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainGlueRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(state.Domain.ValueString())
	glue, diags := r.find(ctx, domain, state.Name.ValueString())
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
		return
	}
	if glue == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.fill(glue)
	state.ID = types.StringValue(domain + "/" + strings.ToLower(state.Name.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *domainGlueRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainGlueRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, name := fqdn(plan.Domain.ValueString()), plan.Name.ValueString()
	glue, diags := r.find(ctx, domain, name)
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
		return
	}
	// The API upserts by name: send the stored spelling so it updates the
	// record instead of adding a second host.
	if glue != nil {
		name = glue.Name
	}
	if resp.Diagnostics.Append(r.upsert(ctx, domain, name, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *domainGlueRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainGlueRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := fqdn(state.Domain.ValueString())
	glue, diags := r.find(ctx, domain, state.Name.ValueString())
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() || glue == nil {
		return
	}
	httpResp, err := r.client.DomainAPI.DomainDomainDnsDestroy(ctx, domain, glue.Name).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(domainError("Deleting glue record "+glue.Name+" of "+domain, httpResp, err))
	}
}

func (r *domainGlueRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("Expected <domain>/<name>, such as example.com/ns1; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}

// find returns the glue record of domain named name, without case, or nil
// when there is none or the account has no such domain. Errors show the
// status alone, as for every domain endpoint.
func (r *domainGlueRecordResource) find(ctx context.Context, domain, name string) (*pidginhost.DNSGlue, diag.Diagnostics) {
	var httpResp *http.Response
	records, err := fetchAll(func(page int32) ([]pidginhost.DNSGlue, bool, error) {
		list, resp, err := r.client.DomainAPI.DomainDomainDnsList(ctx, domain).Page(page).Execute()
		httpResp = resp
		if err != nil {
			return nil, false, err
		}
		return list.Results, list.GetNext() != "", nil
	})
	if isNotFound(httpResp, err) {
		return nil, nil
	}
	if err != nil {
		return nil, diag.Diagnostics{domainError("Reading glue records of "+domain, httpResp, err)}
	}
	return findGlue(records, name), nil
}

func (r *domainGlueRecordResource) upsert(ctx context.Context, domain, name string, m *domainGlueRecordModel) diag.Diagnostics {
	body := pidginhost.NewDNSGlueRequest(name, m.IP.ValueString())
	// An explicit null clears a second address the record had.
	body.Ip2 = *pidginhost.NewNullableString(optionalString(m.IP2))
	_, httpResp, err := r.client.DomainAPI.DomainDomainDnsCreate(ctx, domain).DNSGlueRequest(*body).Execute()
	if err != nil {
		return diag.Diagnostics{domainError("Saving glue record "+name+" of "+domain, httpResp, err)}
	}
	return nil
}

// findGlue returns the record named name, without case, or nil.
func findGlue(records []pidginhost.DNSGlue, name string) *pidginhost.DNSGlue {
	for i, g := range records {
		if strings.EqualFold(g.Name, name) {
			return &records[i]
		}
	}
	return nil
}

// sameAddress compares IP addresses in canonical form, and other text as is.
func sameAddress(a, b string) bool {
	canonical := func(s string) string {
		if addr, err := netip.ParseAddr(s); err == nil {
			return addr.String()
		}
		return s
	}
	return canonical(a) == canonical(b)
}

// fill stores the addresses of g, keeping the configured spelling of each one
// the API stores unchanged.
func (m *domainGlueRecordModel) fill(g *pidginhost.DNSGlue) {
	if m.IP.IsNull() || !sameAddress(m.IP.ValueString(), g.Ip) {
		m.IP = types.StringValue(g.Ip)
	}
	switch ip2 := g.GetIp2(); {
	case ip2 == "":
		m.IP2 = types.StringNull()
	case m.IP2.IsNull() || !sameAddress(m.IP2.ValueString(), ip2):
		m.IP2 = types.StringValue(ip2)
	}
}
