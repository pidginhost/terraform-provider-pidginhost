package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure      = &dnsRecordResource{}
	_ resource.ResourceWithImportState    = &dnsRecordResource{}
	_ resource.ResourceWithValidateConfig = &dnsRecordResource{}
)

func NewDNSRecordResource() resource.Resource { return &dnsRecordResource{} }

type dnsRecordResource struct{ client *pidginhost.APIClient }

type dnsRecordModel struct {
	ID         types.String `tfsdk:"id"`
	Zone       types.String `tfsdk:"zone"`
	Source     types.String `tfsdk:"source"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	TTL        types.Int64  `tfsdk:"ttl"`
	Address    types.String `tfsdk:"address"`
	Cname      types.String `tfsdk:"cname"`
	Exchange   types.String `tfsdk:"exchange"`
	Preference types.Int64  `tfsdk:"preference"`
	Txtdata    types.String `tfsdk:"txtdata"`
	Target     types.String `tfsdk:"target"`
	Priority   types.Int64  `tfsdk:"priority"`
	Weight     types.Int64  `tfsdk:"weight"`
	Port       types.Int64  `tfsdk:"port"`
	Flag       types.Int64  `tfsdk:"flag"`
	Tag        types.String `tfsdk:"tag"`
	Value      types.String `tfsdk:"value"`
}

// dnsTypeFields lists the value fields of each record type in zone file order.
var dnsTypeFields = map[string][]string{
	"A":     {"address"},
	"AAAA":  {"address"},
	"CAA":   {"flag", "tag", "value"},
	"CNAME": {"cname"},
	"MX":    {"preference", "exchange"},
	"SRV":   {"priority", "weight", "port", "target"},
	"TXT":   {"txtdata"},
}

func (r *dnsRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record"
}

func (r *dnsRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	text := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:      true,
			Description:   description,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
	}
	number := func(description string, highest int64) schema.Int64Attribute {
		return schema.Int64Attribute{
			Optional:      true,
			Description:   description,
			Validators:    []validator.Int64{int64validator.Between(0, highest)},
			PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		}
	}
	recordTypes := make([]string, 0, len(dnsTypeFields))
	for t := range dnsTypeFields {
		recordTypes = append(recordTypes, t)
	}
	slices.Sort(recordTypes)
	resp.Schema = schema.Schema{
		Description: "A record in a FreeDNS zone; see pidginhost_dns_zone. The API gives records no id: a record " +
			"is its name, type and the values its type uses, and changing any of them replaces the record. ttl " +
			"changes in place. A and AAAA records use address, CNAME uses cname, MX uses preference and exchange, " +
			"SRV uses priority, weight, port and target, TXT uses txtdata and CAA uses flag, tag and value. Host " +
			"names in cname, exchange and target are compared without case and trailing dot. " +
			"Destroying a record also succeeds if its zone has already been deactivated.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "<zone>/<source>/<fully qualified name>/<type>/<hash of the values>. It changes " +
					"whenever the record is replaced.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone": schema.StringAttribute{
				Required:      true,
				Description:   "Domain name of the zone, such as example.com.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source": dnsSourceAttribute(),
			"name": schema.StringAttribute{
				Required: true,
				Description: "Record name: @ or an empty string for the zone apex, a name relative to the zone " +
					"such as www, or a fully qualified name such as www.example.com. Internationalized names are " +
					"converted to their ASCII form.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:      true,
				Description:   "Record type: A, AAAA, CAA, CNAME, MX, SRV or TXT.",
				Validators:    []validator.String{stringvalidator.OneOf(recordTypes...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ttl": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(14400),
				Description: "Time to live in seconds, from 1 to 2147483647. Defaults to 14400.",
				Validators:  []validator.Int64{int64validator.Between(1, math.MaxInt32)},
			},
			"address":    text("IPv4 address of an A record, or IPv6 address of an AAAA record."),
			"cname":      text("Target host name of a CNAME record."),
			"exchange":   text("Mail server host name of an MX record."),
			"preference": number("Preference of an MX record.", 65535),
			"txtdata":    text("Text of a TXT record, without quotes."),
			"target":     text("Target host name of an SRV record."),
			"priority":   number("Priority of an SRV record.", 65535),
			"weight":     number("Weight of an SRV record.", 65535),
			"port":       number("Port of an SRV record.", 65535),
			"flag":       number("Flag of a CAA record: 0, or 128 for critical.", 255),
			"tag":        text("Tag of a CAA record, such as issue, issuewild or iodef."),
			"value":      text("Value of a CAA record, such as letsencrypt.org."),
		},
	}
}

func (r *dnsRecordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *dnsRecordResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m dnsRecordModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	rtype := m.Type.ValueString()
	if _, known := dnsTypeFields[rtype]; resp.Diagnostics.HasError() || !known {
		return
	}
	missing, extra := dnsFieldProblems(rtype, m.setFields())
	for _, f := range missing {
		resp.Diagnostics.AddAttributeError(path.Root(f), "Missing record value", fmt.Sprintf("A %s record requires %s.", rtype, f))
	}
	for _, f := range extra {
		resp.Diagnostics.AddAttributeError(path.Root(f), "Unused record value", fmt.Sprintf("%s records do not use %s.", rtype, f))
	}
}

func (r *dnsRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer lockDNSZone(plan.zone(), plan.Source.ValueString())()
	records, _, err := r.list(ctx, &plan)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+plan.zone(), err))
		return
	}
	if plan.find(records) != nil {
		resp.Diagnostics.AddError("DNS record already exists", fmt.Sprintf(
			"The zone already has this %s record. Import it with the id %q.", plan.Type.ValueString(), plan.importID()))
		return
	}
	if _, _, err := r.client.FreednsAPI.FreednsDnsAddRecordCreate(ctx).Domain(plan.zone()).Source(plan.Source.ValueString()).
		DNSRecordCreateRequest(plan.request()).Execute(); err != nil {
		resp.Diagnostics.Append(apiError("Adding DNS record", err))
		return
	}
	// Keep the record in state before reading it back, so a failed read
	// taints it instead of leaving it outside Terraform.
	plan.ID = plan.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if records, _, err = r.list(ctx, &plan); err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+plan.zone(), err))
		return
	}
	if plan.find(records) == nil {
		resp.Diagnostics.AddError("DNS record not found after adding it", fmt.Sprintf(
			"The API accepted the %s record %s but lists no record with its values. It may store them in "+
				"another form.", plan.Type.ValueString(), plan.fqname()))
	}
}

func (r *dnsRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	records, httpResp, err := r.list(ctx, &state)
	if isNotFound(httpResp, err) || err != nil && r.zoneGone(ctx, &state) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+state.zone(), err))
		return
	}
	record := state.find(records)
	if record == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.TTL = types.Int64Value(int64(record.Ttl))
	state.ID = state.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update changes the ttl, the only attribute that does not replace the record.
func (r *dnsRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer lockDNSZone(state.zone(), state.Source.ValueString())()
	records, _, err := r.list(ctx, &state)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+state.zone(), err))
		return
	}
	// Line numbers change with every delete: take the current one.
	record := state.find(records)
	if record == nil {
		resp.Diagnostics.AddError("DNS record not found", fmt.Sprintf(
			"The %s record %s is gone from the zone. Refresh and apply again to add it back.", state.Type.ValueString(), state.fqname()))
		return
	}
	body := plan.request()
	body.Line = &record.Line
	if _, _, err := r.client.FreednsAPI.FreednsDnsAddRecordCreate(ctx).Domain(state.zone()).Source(state.Source.ValueString()).
		DNSRecordCreateRequest(body).Execute(); err != nil {
		resp.Diagnostics.Append(apiError("Updating DNS record", err))
		return
	}
	plan.ID = plan.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnsRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer lockDNSZone(state.zone(), state.Source.ValueString())()
	records, httpResp, err := r.list(ctx, &state)
	if isNotFound(httpResp, err) || err != nil && r.zoneGone(ctx, &state) {
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+state.zone(), err))
		return
	}
	record := state.find(records)
	if record == nil {
		return
	}
	_, httpResp, err = r.client.FreednsAPI.FreednsDnsDeleteRecordCreate(ctx).Domain(state.zone()).Source(state.Source.ValueString()).
		DeleteRecordRequest(*pidginhost.NewDeleteRecordRequest(record.Line)).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting DNS record", err))
	}
}

// ImportState finds the record named by the import id, which leaves out the
// values when the zone has one record of that name and type.
func (r *dnsRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	m, values, err := parseDNSRecordImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	records, _, err := r.list(ctx, &m)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading DNS records of "+m.zone(), err))
		return
	}
	found := matchDNSRecords(records, m.zone(), m.fqname(), m.Type.ValueString(), values)
	switch {
	case len(found) == 0:
		resp.Diagnostics.AddError("DNS record not found", fmt.Sprintf("No record in zone %s matches %q.", m.zone(), req.ID))
		return
	case len(found) > 1 && values == nil:
		resp.Diagnostics.AddError("Several DNS records match", fmt.Sprintf(
			"Zone %s has %d %s records named %s. Add the values to the import id, separated by spaces, such as %s/<values>.",
			m.zone(), len(found), m.Type.ValueString(), m.fqname(), req.ID))
		return
	}
	m.fillValues(found[0])
	m.ID = m.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// zoneGone reports a zone whose FreeDNS is no longer active: the API then
// refuses to list its records, and the records went with it.
func (r *dnsRecordResource) zoneGone(ctx context.Context, m *dnsRecordModel) bool {
	zones, _, err := r.client.FreednsAPI.FreednsDnsList(ctx).Execute()
	return err == nil && findDNSZone(zones, m.Zone.ValueString(), m.Source.ValueString()) == nil
}

func (r *dnsRecordResource) list(ctx context.Context, m *dnsRecordModel) ([]pidginhost.DNSRecord, *http.Response, error) {
	return r.client.FreednsAPI.FreednsDnsRecordsList(ctx).Domain(m.zone()).Source(m.Source.ValueString()).Execute()
}

// dnsZoneLocks serializes the record changes of a zone. Writes name records
// by line and a delete renumbers the lines after it, so the list a write
// takes its line from must not interleave with another write.
// ponytail: per provider process; the panel or a second Terraform run editing
// the same zone can still shift lines between a list and a write.
var dnsZoneLocks sync.Map

func lockDNSZone(zone, source string) (unlock func()) {
	v, _ := dnsZoneLocks.LoadOrStore(zone+"/"+source, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// dnsRecordName is the fully qualified record name with a trailing dot, as
// the API lists it: "" and "@" name the zone apex, a name with a trailing dot
// is already qualified, and other names are relative to the zone unless they
// already end with it.
func dnsRecordName(name, zone string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || name == "@":
		return zone + "."
	case strings.HasSuffix(name, "."):
		return fqdn(name) + "."
	}
	name = fqdn(name)
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name + "."
	}
	return name + "." + zone + "."
}

// normalizeDNSValue is the form of a record value used to compare records:
// host names without case and trailing dot, addresses in canonical form.
func normalizeDNSValue(field, value string) string {
	switch field {
	case "cname", "exchange", "target":
		return strings.ToLower(strings.TrimRight(value, "."))
	case "address":
		if addr, err := netip.ParseAddr(value); err == nil {
			return addr.String()
		}
	}
	return value
}

// dnsRecordKey identifies a record by its fully qualified name, its type and
// the normalized values its type uses.
func dnsRecordKey(fqname, rtype string, values map[string]string) string {
	parts := []string{fqname, rtype}
	for _, f := range dnsTypeFields[rtype] {
		parts = append(parts, normalizeDNSValue(f, values[f]))
	}
	return strings.Join(parts, "\x00")
}

// dnsRecordType is the record type as the configuration spells it: the API
// sends and lists CAA records as TYPE257.
func dnsRecordType(apiType string) string {
	if apiType == "TYPE257" {
		return "CAA"
	}
	return apiType
}

// apiDNSValues returns the value fields of an API record, "" when unset.
func apiDNSValues(r pidginhost.DNSRecord) map[string]string {
	number := func(n *int32) string {
		if n == nil {
			return ""
		}
		return strconv.Itoa(int(*n))
	}
	return map[string]string{
		"address": r.GetAddress(), "cname": r.GetCname(), "exchange": r.GetExchange(), "preference": number(r.Preference),
		"txtdata": r.GetTxtdata(), "target": r.GetTarget(), "priority": number(r.Priority), "weight": number(r.Weight),
		"port": number(r.Port), "flag": number(r.Flag), "tag": r.GetTag(), "value": r.GetValue(),
	}
}

// matchDNSRecords returns the records of zone with the given name and type
// and, unless values is nil, the given values.
func matchDNSRecords(records []pidginhost.DNSRecord, zone, fqname, rtype string, values map[string]string) []pidginhost.DNSRecord {
	var out []pidginhost.DNSRecord
	for _, r := range records {
		if dnsRecordName(r.Name, zone) != fqname || dnsRecordType(r.Type) != rtype {
			continue
		}
		if values != nil && dnsRecordKey(fqname, rtype, apiDNSValues(r)) != dnsRecordKey(fqname, rtype, values) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// dnsFieldProblems returns the value fields a record of rtype needs and set
// lacks, and the fields set has that rtype does not use.
func dnsFieldProblems(rtype string, set map[string]bool) (missing, extra []string) {
	uses := dnsTypeFields[rtype]
	for _, f := range uses {
		if !set[f] {
			missing = append(missing, f)
		}
	}
	for f, ok := range set {
		if ok && !slices.Contains(uses, f) {
			extra = append(extra, f)
		}
	}
	slices.Sort(extra)
	return missing, extra
}

// parseDNSRecordImportID reads "<zone>/<source>/<name>/<type>[/<values>]",
// where values are the type's values in zone file order, separated by
// spaces. values is nil when the id has none.
func parseDNSRecordImportID(id string) (dnsRecordModel, map[string]string, error) {
	usage := fmt.Errorf("expected <zone>/<source>/<name>/<type>[/<values>], such as "+
		"example.com/internal/www/A/192.0.2.10 or example.com/internal/@/MX/10 mail.example.com; got %q", id)
	parts := strings.SplitN(id, "/", 5)
	if len(parts) < 4 || parts[0] == "" || (parts[1] != "internal" && parts[1] != "external") {
		return dnsRecordModel{}, nil, usage
	}
	rtype := strings.ToUpper(parts[3])
	fields, ok := dnsTypeFields[rtype]
	if !ok {
		return dnsRecordModel{}, nil, usage
	}
	m := dnsRecordModel{Zone: types.StringValue(parts[0]), Source: types.StringValue(parts[1]),
		Name: types.StringValue(parts[2]), Type: types.StringValue(rtype)}
	if len(parts) == 4 {
		return m, nil, nil
	}
	given := strings.SplitN(parts[4], " ", len(fields))
	if len(given) != len(fields) {
		return dnsRecordModel{}, nil, fmt.Errorf("%s records take %d values (%s); got %q",
			rtype, len(fields), strings.Join(fields, ", "), parts[4])
	}
	values := map[string]string{}
	for i, f := range fields {
		values[f] = given[i]
	}
	return m, values, nil
}

func (m *dnsRecordModel) zone() string   { return fqdn(m.Zone.ValueString()) }
func (m *dnsRecordModel) fqname() string { return dnsRecordName(m.Name.ValueString(), m.zone()) }

// fields returns pointers to the value fields, by name.
func (m *dnsRecordModel) fields() map[string]any {
	return map[string]any{
		"address": &m.Address, "cname": &m.Cname, "exchange": &m.Exchange, "preference": &m.Preference,
		"txtdata": &m.Txtdata, "target": &m.Target, "priority": &m.Priority, "weight": &m.Weight,
		"port": &m.Port, "flag": &m.Flag, "tag": &m.Tag, "value": &m.Value,
	}
}

// values returns the value fields as the API lists them, "" when unset.
func (m *dnsRecordModel) values() map[string]string {
	out := map[string]string{}
	for name, f := range m.fields() {
		switch f := f.(type) {
		case *types.String:
			out[name] = f.ValueString()
		case *types.Int64:
			if !f.IsNull() && !f.IsUnknown() {
				out[name] = strconv.FormatInt(f.ValueInt64(), 10)
			}
		}
	}
	return out
}

// setFields reports which value fields are set; unknown values count as set.
func (m *dnsRecordModel) setFields() map[string]bool {
	out := map[string]bool{}
	for name, f := range m.fields() {
		out[name] = !f.(attr.Value).IsNull()
	}
	return out
}

// fillValues stores the ttl and the normalized values of the API record.
func (m *dnsRecordModel) fillValues(r pidginhost.DNSRecord) {
	values := apiDNSValues(r)
	for _, name := range dnsTypeFields[m.Type.ValueString()] {
		value := normalizeDNSValue(name, values[name])
		switch f := m.fields()[name].(type) {
		case *types.String:
			*f = types.StringValue(value)
		case *types.Int64:
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				*f = types.Int64Value(n)
			}
		}
	}
	m.TTL = types.Int64Value(int64(r.Ttl))
}

// find returns the record with the name, type and values of m, or nil.
func (m *dnsRecordModel) find(records []pidginhost.DNSRecord) *pidginhost.DNSRecord {
	found := matchDNSRecords(records, m.zone(), m.fqname(), m.Type.ValueString(), m.values())
	if len(found) == 0 {
		return nil
	}
	return &found[0]
}

func (m *dnsRecordModel) id() types.String {
	rtype := m.Type.ValueString()
	sum := sha256.Sum256([]byte(dnsRecordKey(m.fqname(), rtype, m.values())))
	return types.StringValue(fmt.Sprintf("%s/%s/%s/%s/%x",
		m.zone(), m.Source.ValueString(), strings.TrimSuffix(m.fqname(), "."), rtype, sum[:6]))
}

// importID is the import id that names this record.
func (m *dnsRecordModel) importID() string {
	values := m.values()
	var given []string
	for _, f := range dnsTypeFields[m.Type.ValueString()] {
		given = append(given, values[f])
	}
	name := m.Name.ValueString()
	if name == "" {
		name = "@"
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s", m.Zone.ValueString(), m.Source.ValueString(), name, m.Type.ValueString(), strings.Join(given, " "))
}

func (m *dnsRecordModel) request() pidginhost.DNSRecordCreateRequest {
	rtype := m.Type.ValueString()
	apiType := rtype
	if rtype == "CAA" {
		apiType = "TYPE257"
	}
	body := pidginhost.NewDNSRecordCreateRequest(m.fqname(), int32(m.TTL.ValueInt64()), pidginhost.DNSRecordCreateTypeEnum(apiType))
	body.Address = optionalString(m.Address)
	body.Cname = optionalString(m.Cname)
	body.Exchange = optionalString(m.Exchange)
	body.Preference = optionalInt32(m.Preference)
	body.Txtdata = optionalString(m.Txtdata)
	body.Target = optionalString(m.Target)
	body.Priority = optionalInt32(m.Priority)
	body.Weight = optionalInt32(m.Weight)
	body.Port = optionalInt32(m.Port)
	body.Flag = optionalInt32(m.Flag)
	body.Tag = optionalString(m.Tag)
	body.Value = optionalString(m.Value)
	if rtype == "TXT" {
		body.Unencoded = pidginhost.PtrString("1")
	}
	return *body
}
