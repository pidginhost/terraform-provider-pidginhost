package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &firewallResource{}
	_ resource.ResourceWithImportState = &firewallResource{}
)

func NewFirewallResource() resource.Resource { return &firewallResource{} }

type firewallResource struct{ client *pidginhost.APIClient }

type firewallModel struct {
	ID     types.String        `tfsdk:"id"`
	Name   types.String        `tfsdk:"name"`
	Status types.String        `tfsdk:"status"`
	Rules  []firewallRuleModel `tfsdk:"rule"`
}

type firewallRuleModel struct {
	Direction   types.String `tfsdk:"direction"`
	Action      types.String `tfsdk:"action"`
	Protocol    types.String `tfsdk:"protocol"`
	Source      types.String `tfsdk:"source"`
	Sport       types.String `tfsdk:"sport"`
	Destination types.String `tfsdk:"destination"`
	Dport       types.String `tfsdk:"dport"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Position    types.Int64  `tfsdk:"position"`
}

func (r *firewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall"
}

func (r *firewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	needsProtocol := stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("protocol"))
	notEmpty := stringvalidator.LengthAtLeast(1)
	resp.Schema = schema.Schema{
		Description: "A firewall rules set. Assign it to a server's public interface with pidginhost_server.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the rules set.",
				Validators:  []validator.String{notEmpty},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Validation status of the rules: validated, invalid or pending.",
			},
			"rule": schema.ListNestedAttribute{
				Optional: true,
				Description: "Rules of the set. The list is authoritative: rules the set has but the list does not " +
					"are deleted. A rule is identified by its direction, action, protocol, addresses and ports; " +
					"changing any of these replaces that rule, while enabled and position change in place.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"direction": schema.StringAttribute{
							Required:    true,
							Description: "Traffic direction: in or out.",
							Validators:  []validator.String{stringvalidator.OneOf("in", "out")},
						},
						"action": schema.StringAttribute{
							Required:    true,
							Description: "What happens to matching traffic: ACCEPT, DROP or REJECT.",
							Validators:  []validator.String{stringvalidator.OneOf("ACCEPT", "DROP", "REJECT")},
						},
						"protocol": schema.StringAttribute{
							Optional:    true,
							Description: "Protocol, such as tcp, udp or icmp. Unset matches any protocol.",
							Validators:  []validator.String{notEmpty},
						},
						"source": schema.StringAttribute{
							Optional: true,
							Description: "Source address: a single IP, a range such as 192.0.2.10-192.0.2.20, " +
								"or a comma separated list. Unset matches any source.",
							Validators: []validator.String{notEmpty},
						},
						"sport": schema.StringAttribute{
							Optional: true,
							Description: "Source port: a number, a range such as 80:85, or a comma separated list. " +
								"Requires protocol.",
							Validators: []validator.String{notEmpty, needsProtocol},
						},
						"destination": schema.StringAttribute{
							Optional: true,
							Description: "Destination address: a single IP, a range such as 192.0.2.10-192.0.2.20, " +
								"or a comma separated list. Unset matches any destination.",
							Validators: []validator.String{notEmpty},
						},
						"dport": schema.StringAttribute{
							Optional: true,
							Description: "Destination port: a number, a range such as 80:85, or a comma separated " +
								"list. Requires protocol.",
							Validators: []validator.String{notEmpty, needsProtocol},
						},
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(true),
							Description: "Whether the rule applies. Defaults to true.",
						},
						"position": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Description: "Order of the rule in the set. The API assigns one when left empty.",
						},
					},
				},
			},
		},
	}
}

func (r *firewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *firewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	set, _, err := r.client.CloudAPI.CloudFirewallRulesSetCreate(ctx).
		FirewallRulesSetRequest(*pidginhost.NewFirewallRulesSetRequest(plan.Name.ValueString())).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating firewall rules set", err))
		return
	}
	// Keep the set in state before adding rules, so a refused rule taints it
	// instead of leaving it outside Terraform.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.Itoa(int(set.Id)))...)
	if set, err = r.applyRules(ctx, set, plan.Rules); err != nil {
		resp.Diagnostics.Append(apiError("Creating firewall rules", err))
		return
	}
	plan.fill(set)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *firewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall id", err.Error())
		return
	}
	set, httpResp, err := r.client.CloudAPI.CloudFirewallRulesSetRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading firewall rules set", err))
		return
	}
	state.fill(set)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall id", err.Error())
		return
	}
	var set *pidginhost.FirewallRulesSet
	if !plan.Name.Equal(state.Name) {
		body := pidginhost.PatchedFirewallRulesSetRequest{Name: optionalString(plan.Name)}
		set, _, err = r.client.CloudAPI.CloudFirewallRulesSetPartialUpdate(ctx, id).PatchedFirewallRulesSetRequest(body).Execute()
	} else {
		set, _, err = r.client.CloudAPI.CloudFirewallRulesSetRetrieve(ctx, id).Execute()
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Updating firewall rules set", err))
		return
	}
	if set, err = r.applyRules(ctx, set, plan.Rules); err != nil {
		resp.Diagnostics.Append(apiError("Updating firewall rules", err))
		return
	}
	plan.fill(set)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *firewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall id", err.Error())
		return
	}
	httpResp, err := r.client.CloudAPI.CloudFirewallRulesSetDestroy(ctx, id).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting firewall rules set", err))
	}
}

func (r *firewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// applyRules brings the rules of set to desired, deleting first, then
// patching, then creating, and returns the set as the API now has it.
func (r *firewallResource) applyRules(ctx context.Context, set *pidginhost.FirewallRulesSet, desired []firewallRuleModel) (*pidginhost.FirewallRulesSet, error) {
	want := make([]pidginhost.FirewallRuleRequest, len(desired))
	for i, m := range desired {
		want[i] = m.request()
	}
	del, patch, create := diffFirewallRules(set.Rules, want)
	setID := strconv.Itoa(int(set.Id))
	for _, id := range del {
		httpResp, err := r.client.CloudAPI.CloudFirewallRulesSetRulesDestroy(ctx, strconv.Itoa(int(id)), setID).Execute()
		if err != nil && !isNotFound(httpResp, err) {
			return nil, err
		}
	}
	for _, p := range patch {
		if _, _, err := r.client.CloudAPI.CloudFirewallRulesSetRulesPartialUpdate(ctx, strconv.Itoa(int(p.id)), setID).
			PatchedFirewallRuleRequest(p.body).Execute(); err != nil {
			return nil, err
		}
	}
	for _, c := range create {
		if _, _, err := r.client.CloudAPI.CloudFirewallRulesSetRulesCreate(ctx, setID).FirewallRuleRequest(c).Execute(); err != nil {
			return nil, err
		}
	}
	if len(del)+len(patch)+len(create) == 0 && set.Status != pidginhost.FIREWALLRULESSETSTATUSENUM_PENDING {
		return set, nil
	}
	return r.waitValidated(ctx, set.Id)
}

// waitValidated waits while the API validates a rule set, which it does in
// the background after every change, and fails when it rejects a rule.
func (r *firewallResource) waitValidated(ctx context.Context, id int32) (*pidginhost.FirewallRulesSet, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var set *pidginhost.FirewallRulesSet
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		got, _, err := r.client.CloudAPI.CloudFirewallRulesSetRetrieve(ctx, id).Execute()
		if err != nil {
			return false, err
		}
		set = got
		return got.Status != pidginhost.FIREWALLRULESSETSTATUSENUM_PENDING, nil
	})
	if err != nil {
		return nil, err
	}
	if set.Status == pidginhost.FIREWALLRULESSETSTATUSENUM_INVALID {
		var problems []string
		for _, rule := range set.Rules {
			if rule.HasError {
				problems = append(problems, fmt.Sprintf("rule %d: %s", rule.Id, rule.ErrorMessage))
			}
		}
		return set, fmt.Errorf("the API rejected the rule set: %s", strings.Join(problems, "; "))
	}
	return set, nil
}

type firewallRulePatch struct {
	id   int32
	body pidginhost.PatchedFirewallRuleRequest
}

// firewallRuleKey identifies a rule: direction, action, protocol, source,
// sport, destination and dport, with an unset value equal to "".
type firewallRuleKey [7]string

func ruleKey(direction pidginhost.FirewallRuleDirectionEnum, action pidginhost.FwPolicyOutEnum, fields ...*string) firewallRuleKey {
	key := firewallRuleKey{string(direction), string(action)}
	for i, f := range fields {
		if f != nil {
			key[i+2] = *f
		}
	}
	return key
}

func existingRuleKey(r pidginhost.FirewallRule) firewallRuleKey {
	return ruleKey(r.Direction, r.Action, r.Protocol, r.Source, r.Sport, r.Destination, r.Dport)
}

func desiredRuleKey(r pidginhost.FirewallRuleRequest) firewallRuleKey {
	return ruleKey(r.Direction, r.Action, r.Protocol, r.Source, r.Sport, r.Destination, r.Dport)
}

// diffFirewallRules matches each desired rule with one existing rule of the
// same key. Matched rules are patched where enabled or position differ and
// are set; unmatched desired rules are created; unmatched existing rules are
// deleted.
func diffFirewallRules(existing []pidginhost.FirewallRule, desired []pidginhost.FirewallRuleRequest) (del []int32, patch []firewallRulePatch, create []pidginhost.FirewallRuleRequest) {
	unmatched := slices.Clone(existing)
	for _, want := range desired {
		i := slices.IndexFunc(unmatched, func(have pidginhost.FirewallRule) bool { return existingRuleKey(have) == desiredRuleKey(want) })
		if i < 0 {
			create = append(create, want)
			continue
		}
		have := unmatched[i]
		unmatched = slices.Delete(unmatched, i, i+1)
		var body pidginhost.PatchedFirewallRuleRequest
		if want.Enabled != nil && (have.Enabled == nil || *have.Enabled != *want.Enabled) {
			body.Enabled = want.Enabled
		}
		if want.Position != nil && (have.Position == nil || *have.Position != *want.Position) {
			body.Position = want.Position
		}
		if body.Enabled != nil || body.Position != nil {
			patch = append(patch, firewallRulePatch{id: have.Id, body: body})
		}
	}
	for _, r := range unmatched {
		del = append(del, r.Id)
	}
	return del, patch, create
}

// orderFirewallRules lists the API's rules in the order of prior (the
// configured or stored list), then the rules prior does not have, so a
// refresh only shows real changes.
func orderFirewallRules(prior []firewallRuleModel, rules []pidginhost.FirewallRule) []firewallRuleModel {
	unmatched := slices.Clone(rules)
	out := make([]firewallRuleModel, 0, len(rules))
	for _, p := range prior {
		key := desiredRuleKey(p.request())
		i := slices.IndexFunc(unmatched, func(r pidginhost.FirewallRule) bool { return existingRuleKey(r) == key })
		if i >= 0 {
			out = append(out, firewallRuleFromAPI(unmatched[i]))
			unmatched = slices.Delete(unmatched, i, i+1)
		}
	}
	for _, r := range unmatched {
		out = append(out, firewallRuleFromAPI(r))
	}
	if len(out) == 0 && prior == nil {
		return nil
	}
	return out
}

func (m firewallRuleModel) request() pidginhost.FirewallRuleRequest {
	return pidginhost.FirewallRuleRequest{
		Direction:   pidginhost.FirewallRuleDirectionEnum(m.Direction.ValueString()),
		Action:      pidginhost.FwPolicyOutEnum(m.Action.ValueString()),
		Protocol:    optionalString(m.Protocol),
		Source:      optionalString(m.Source),
		Sport:       optionalString(m.Sport),
		Destination: optionalString(m.Destination),
		Dport:       optionalString(m.Dport),
		Enabled:     optionalBool(m.Enabled),
		Position:    optionalInt32(m.Position),
	}
}

func firewallRuleFromAPI(r pidginhost.FirewallRule) firewallRuleModel {
	m := firewallRuleModel{
		Direction:   types.StringValue(string(r.Direction)),
		Action:      types.StringValue(string(r.Action)),
		Protocol:    nonEmptyString(r.Protocol),
		Source:      nonEmptyString(r.Source),
		Sport:       nonEmptyString(r.Sport),
		Destination: nonEmptyString(r.Destination),
		Dport:       nonEmptyString(r.Dport),
		// The API enables a rule unless told otherwise.
		Enabled:  types.BoolValue(r.Enabled == nil || *r.Enabled),
		Position: types.Int64Null(),
	}
	if r.Position != nil {
		m.Position = types.Int64Value(int64(*r.Position))
	}
	return m
}

// nonEmptyString maps an unset or empty API string to null.
func nonEmptyString(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func (m *firewallModel) fill(set *pidginhost.FirewallRulesSet) {
	m.ID = types.StringValue(strconv.Itoa(int(set.Id)))
	m.Name = types.StringValue(set.Name)
	m.Status = types.StringValue(string(set.Status))
	m.Rules = orderFirewallRules(m.Rules, set.Rules)
}
