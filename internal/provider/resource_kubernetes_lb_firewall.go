package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &kubernetesLBFirewallResource{}
	_ resource.ResourceWithImportState = &kubernetesLBFirewallResource{}
)

func NewKubernetesLBFirewallResource() resource.Resource { return &kubernetesLBFirewallResource{} }

type kubernetesLBFirewallResource struct{ client *pidginhost.APIClient }

type kubernetesLBFirewallModel struct {
	ID        types.String          `tfsdk:"id"`
	ClusterID types.String          `tfsdk:"cluster_id"`
	Rules     []lbFirewallRuleModel `tfsdk:"rule"`
}

type lbFirewallRuleModel struct {
	Direction   types.String `tfsdk:"direction"`
	Action      types.String `tfsdk:"action"`
	Protocol    types.String `tfsdk:"protocol"`
	Source      types.String `tfsdk:"source"`
	Sport       types.String `tfsdk:"sport"`
	Destination types.String `tfsdk:"destination"`
	Dport       types.String `tfsdk:"dport"`
	Comment     types.String `tfsdk:"comment"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Position    types.Int64  `tfsdk:"position"`
}

// lbRule is a load balancer firewall rule as the diff sees it: an unset
// string is "".
type lbRule struct {
	ID                                                             int32
	Direction, Action, Protocol, Source, Sport, Destination, Dport string
	Comment                                                        string
	Enabled                                                        bool
	Position                                                       int32
}

// key is what identifies a rule; comment, enabled and position change in place.
func (r lbRule) key() [7]string {
	return [7]string{r.Direction, r.Action, r.Protocol, r.Source, r.Sport, r.Destination, r.Dport}
}

func (r *kubernetesLBFirewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_lb_firewall"
}

func (r *kubernetesLBFirewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	optional := func(description string, validators ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:    true,
			Description: description,
			Validators:  append([]validator.String{stringvalidator.LengthAtLeast(1)}, validators...),
		}
	}
	needsProtocol := stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("protocol"))
	resp.Schema = schema.Schema{
		Description: "The firewall rules of the load balancer of a Kubernetes cluster. The rule list is authoritative: " +
			"rules the load balancer has that are not listed are deleted, and destroying the resource deletes all rules.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Same as cluster_id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": clusterIDAttribute(),
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "A firewall rule. Rules match existing ones on direction, action, protocol, addresses " +
					"and ports; comment, enabled and position change in place.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"direction": schema.StringAttribute{
							Required:    true,
							Description: "Traffic direction: in or out.",
							Validators:  []validator.String{stringvalidator.OneOf("in", "out")},
						},
						"action": schema.StringAttribute{
							Required:    true,
							Description: "Action for matching traffic: ACCEPT, DROP or REJECT.",
							Validators:  []validator.String{stringvalidator.OneOf("ACCEPT", "DROP", "REJECT")},
						},
						"protocol":    optional("Protocol, such as tcp, udp or icmp. Required with sport or dport."),
						"source":      optional("Source IP address or CIDR."),
						"sport":       optional("Source port or range, such as 1024-65535.", needsProtocol),
						"destination": optional("Destination IP address or CIDR."),
						"dport":       optional("Destination port or range, such as 80 or 8000-9000.", needsProtocol),
						"comment":     optional("Free text comment."),
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(true),
							Description: "Whether the rule is active. Defaults to true.",
						},
						"position": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Default:     int64default.StaticInt64(100),
							Description: "Rule order, lower runs first. Defaults to 100.",
						},
					},
				},
			},
		},
	}
}

func (r *kubernetesLBFirewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *kubernetesLBFirewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesLBFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Creating Kubernetes load balancer firewall", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesLBFirewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesLBFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := int32ID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	rules, found, err := listLBRules(ctx, r.client, clusterID)
	if err != nil {
		resp.Diagnostics.AddError("Reading Kubernetes load balancer firewall", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	state.setRules(orderLBRules(rules, state.rules()))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *kubernetesLBFirewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kubernetesLBFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Updating Kubernetes load balancer firewall", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesLBFirewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesLBFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Rules = nil
	if err := r.apply(ctx, &state); err != nil && !errors.Is(err, errClusterGone) {
		resp.Diagnostics.AddError("Deleting Kubernetes load balancer firewall", err.Error())
	}
}

func (r *kubernetesLBFirewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := int32ID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected the cluster ID, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

var errClusterGone = errors.New("the Kubernetes cluster no longer exists")

// apply makes the load balancer rules match m.Rules, deleting first, then
// patching, then creating, and refreshes m from the result.
func (r *kubernetesLBFirewallResource) apply(ctx context.Context, m *kubernetesLBFirewallModel) error {
	clusterID, err := int32ID(m.ClusterID.ValueString())
	if err != nil {
		return err
	}
	desired := m.rules()
	existing, found, err := listLBRules(ctx, r.client, clusterID)
	if err == nil && !found {
		err = errClusterGone
	}
	if err != nil {
		return err
	}
	api := r.client.KubernetesAPI
	create, patch, remove := diffLBRules(existing, desired)
	for _, rule := range remove {
		httpResp, err := api.KubernetesClustersLbFirewallDestroy(ctx, clusterID, strconv.Itoa(int(rule.ID))).Execute()
		if err != nil && !isNotFound(httpResp, err) {
			return fmt.Errorf("deleting rule %d: %s", rule.ID, describeError(err))
		}
	}
	for _, rule := range patch {
		body := pidginhost.PatchedLBFirewallRuleRequest{Comment: &rule.Comment, Enabled: &rule.Enabled, Position: &rule.Position}
		if _, _, err := api.KubernetesClustersLbFirewallPartialUpdate(ctx, clusterID, strconv.Itoa(int(rule.ID))).PatchedLBFirewallRuleRequest(body).Execute(); err != nil {
			return fmt.Errorf("updating rule %d: %s", rule.ID, describeError(err))
		}
	}
	for _, rule := range create {
		if _, _, err := api.KubernetesClustersLbFirewallCreate(ctx, clusterID).LBFirewallRuleRequest(rule.request()).Execute(); err != nil {
			return fmt.Errorf("creating a rule: %s", describeError(err))
		}
	}
	after, found, err := listLBRules(ctx, r.client, clusterID)
	if err == nil && !found {
		err = errClusterGone
	}
	if err != nil {
		return err
	}
	m.ID = m.ClusterID
	m.setRules(orderLBRules(after, desired))
	return nil
}

// diffLBRules matches the desired rules one to one with the existing rules on
// their key. It returns the rules to create, the matched rules whose comment,
// enabled or position differ (desired values with the existing ID), and the
// existing rules nothing matched, which the authoritative list deletes.
func diffLBRules(existing, desired []lbRule) (create, patch, remove []lbRule) {
	unmatched := slices.Clone(existing)
	for _, want := range desired {
		i := slices.IndexFunc(unmatched, func(r lbRule) bool { return r.key() == want.key() })
		if i < 0 {
			create = append(create, want)
			continue
		}
		have := unmatched[i]
		unmatched = slices.Delete(unmatched, i, i+1)
		want.ID = have.ID
		if want != have {
			patch = append(patch, want)
		}
	}
	return create, patch, unmatched
}

// orderLBRules lists the existing rules in the order of the rules they match
// in prior, then the rest in API order, so the state keeps the configured
// order and extra rules show up at the end.
func orderLBRules(existing, prior []lbRule) []lbRule {
	rest := slices.Clone(existing)
	ordered := make([]lbRule, 0, len(existing))
	for _, p := range prior {
		if i := slices.IndexFunc(rest, func(r lbRule) bool { return r.key() == p.key() }); i >= 0 {
			ordered = append(ordered, rest[i])
			rest = slices.Delete(rest, i, i+1)
		}
	}
	return append(ordered, rest...)
}

// listLBRules reads every rule of the cluster load balancer. It reports false
// when the cluster is gone.
func listLBRules(ctx context.Context, client *pidginhost.APIClient, clusterID int32) ([]lbRule, bool, error) {
	found := true
	rules, err := fetchAll(func(page int32) ([]pidginhost.LBFirewallRule, bool, error) {
		list, httpResp, err := client.KubernetesAPI.KubernetesClustersLbFirewallList(ctx, clusterID).Page(page).Execute()
		if isNotFound(httpResp, err) {
			found = false
			return nil, false, nil
		}
		if err != nil {
			return nil, false, errors.New(describeError(err))
		}
		return list.Results, list.GetNext() != "", nil
	})
	out := make([]lbRule, len(rules))
	for i, r := range rules {
		out[i] = lbRule{
			ID: r.Id, Direction: string(r.GetDirection()), Action: string(r.GetAction()),
			Protocol: r.GetProtocol(), Source: r.GetSource(), Sport: r.GetSport(),
			Destination: r.GetDestination(), Dport: r.GetDport(), Comment: r.GetComment(),
			Enabled: r.GetEnabled(), Position: r.GetPosition(),
		}
	}
	return out, found, err
}

func (r lbRule) request() pidginhost.LBFirewallRuleRequest {
	direction := pidginhost.LBFirewallRuleDirectionEnum(r.Direction)
	action := pidginhost.LBFirewallRuleActionEnum(r.Action)
	set := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	return pidginhost.LBFirewallRuleRequest{
		Direction: &direction, Action: &action,
		Protocol: set(r.Protocol), Source: set(r.Source), Sport: set(r.Sport),
		Destination: set(r.Destination), Dport: set(r.Dport), Comment: set(r.Comment),
		Enabled: &r.Enabled, Position: &r.Position,
	}
}

func (m *kubernetesLBFirewallModel) rules() []lbRule {
	rules := make([]lbRule, len(m.Rules))
	for i, r := range m.Rules {
		rules[i] = lbRule{
			Direction: r.Direction.ValueString(), Action: r.Action.ValueString(),
			Protocol: r.Protocol.ValueString(), Source: r.Source.ValueString(), Sport: r.Sport.ValueString(),
			Destination: r.Destination.ValueString(), Dport: r.Dport.ValueString(), Comment: r.Comment.ValueString(),
			Enabled: r.Enabled.ValueBool(), Position: int32(r.Position.ValueInt64()),
		}
	}
	return rules
}

func (m *kubernetesLBFirewallModel) setRules(rules []lbRule) {
	optional := func(s string) types.String {
		if s == "" {
			return types.StringNull()
		}
		return types.StringValue(s)
	}
	// Never nil: zero rule blocks are an empty list, not a null one.
	m.Rules = make([]lbFirewallRuleModel, len(rules))
	for i, r := range rules {
		m.Rules[i] = lbFirewallRuleModel{
			Direction: types.StringValue(r.Direction), Action: types.StringValue(r.Action),
			Protocol: optional(r.Protocol), Source: optional(r.Source), Sport: optional(r.Sport),
			Destination: optional(r.Destination), Dport: optional(r.Dport), Comment: optional(r.Comment),
			Enabled: types.BoolValue(r.Enabled), Position: types.Int64Value(int64(r.Position)),
		}
	}
}
