package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &serverResource{}
var _ resource.ResourceWithConfigure = &serverResource{}
var _ resource.ResourceWithImportState = &serverResource{}

func NewServerResource() resource.Resource {
	return &serverResource{}
}

type serverResource struct {
	client *Client
}

type serverResourceModel struct {
	ID                      types.String        `tfsdk:"id"`
	Image                   types.String        `tfsdk:"image"`
	Package                 types.String        `tfsdk:"package"`
	Hostname                types.String        `tfsdk:"hostname"`
	Project                 types.String        `tfsdk:"project"`
	Password                types.String        `tfsdk:"password"`
	SSHPubKey               types.String        `tfsdk:"ssh_pub_key"`
	SSHPubKeyID             types.String        `tfsdk:"ssh_pub_key_id"`
	PublicIP                types.String        `tfsdk:"public_ip"`
	NewIPv4                 types.Bool          `tfsdk:"new_ipv4"`
	PublicIPv6              types.String        `tfsdk:"public_ipv6"`
	NewIPv6                 types.Bool          `tfsdk:"new_ipv6"`
	DestroyPublicIPv4       types.Bool          `tfsdk:"destroy_public_ipv4"`
	DestroyPublicIPv6       types.Bool          `tfsdk:"destroy_public_ipv6"`
	FwRulesSet              types.String        `tfsdk:"fw_rules_set"`
	FwPolicyIn              types.String        `tfsdk:"fw_policy_in"`
	FwPolicyOut             types.String        `tfsdk:"fw_policy_out"`
	PrivateNetwork          types.String        `tfsdk:"private_network"`
	PrivateAddress          types.String        `tfsdk:"private_address"`
	NoNetworkAcknowledged   types.Bool          `tfsdk:"no_network_acknowledged"`
	FirewallRulesCreate     types.Bool          `tfsdk:"firewall_rules_create"`
	FirewallRulesSetName    types.String        `tfsdk:"firewall_rules_set_name"`
	FirewallRules           []firewallRuleModel `tfsdk:"firewall_rules"`
	DestroyFirewallRulesSet types.Bool          `tfsdk:"destroy_firewall_rules_set"`
	FirewallRulesSetID      types.String        `tfsdk:"firewall_rules_set_id"`
	ExtraVolumeProduct      types.String        `tfsdk:"extra_volume_product"`
	ExtraVolumeAlias        types.String        `tfsdk:"extra_volume_alias"`
	ExtraVolumeSize         types.Int64         `tfsdk:"extra_volume_size"`
	DestroyExtraVolume      types.Bool          `tfsdk:"destroy_extra_volume"`
	ExtraVolumeID           types.String        `tfsdk:"extra_volume_id"`
	WaitForReadySeconds     types.Int64         `tfsdk:"wait_for_ready_seconds"`
	Status                  types.String        `tfsdk:"status"`
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
	Position    types.String `tfsdk:"position"`
}

func (r *serverResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *serverResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a PidginHost server.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Unique identifier of the server.",
			},
			"image": schema.StringAttribute{
				Required:    true,
				Description: "Image identifier or slug.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"package": schema.StringAttribute{
				Required:    true,
				Description: "Package identifier or slug.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"hostname": schema.StringAttribute{
				Required:    true,
				Description: "Hostname assigned to the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"project": schema.StringAttribute{
				Optional:    true,
				Description: "Optional project label.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Root/administrator password.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ssh_pub_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "SSH public key material.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ssh_pub_key_id": schema.StringAttribute{
				Optional:    true,
				Description: "Identifier of an existing SSH key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"public_ip": schema.StringAttribute{
				Optional:    true,
				Description: "Existing public IPv4 to attach.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"new_ipv4": schema.BoolAttribute{
				Optional:    true,
				Description: "Request allocation of a new public IPv4.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"public_ipv6": schema.StringAttribute{
				Optional:    true,
				Description: "Existing public IPv6 to attach.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"new_ipv6": schema.BoolAttribute{
				Optional:    true,
				Description: "Request allocation of a new public IPv6.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"destroy_public_ipv4": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "When true the attached public IPv4 address is deleted during destroy; when false it is detached and kept.",
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"destroy_public_ipv6": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "When true the attached public IPv6 address is deleted during destroy; when false it is detached and kept.",
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"fw_rules_set": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Firewall ruleset slug.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fw_policy_in": schema.StringAttribute{
				Optional:    true,
				Description: "Firewall policy for inbound traffic.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fw_policy_out": schema.StringAttribute{
				Optional:    true,
				Description: "Firewall policy for outbound traffic.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"private_network": schema.StringAttribute{
				Optional:    true,
				Description: "Private network identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"private_address": schema.StringAttribute{
				Optional:    true,
				Description: "Static private IP when attaching a private network.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"firewall_rules_create": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Set true to create a new firewall rules set for this server.",
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"firewall_rules_set_name": schema.StringAttribute{
				Optional:    true,
				Description: "Name of the firewall rules set to create or reference.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"firewall_rules": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Optional list of firewall rules to add when creating a new rules set.",
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"direction": schema.StringAttribute{
							Optional:    true,
							Description: "Direction of traffic (in/out).",
						},
						"action": schema.StringAttribute{
							Optional:    true,
							Description: "Action to take (ACCEPT, DROP, REJECT).",
						},
						"protocol": schema.StringAttribute{
							Optional:    true,
							Description: "Protocol (tcp, udp, etc).",
						},
						"source": schema.StringAttribute{
							Optional:    true,
							Description: "Source CIDR.",
						},
						"sport": schema.StringAttribute{
							Optional:    true,
							Description: "Source port.",
						},
						"destination": schema.StringAttribute{
							Optional:    true,
							Description: "Destination CIDR.",
						},
						"dport": schema.StringAttribute{
							Optional:    true,
							Description: "Destination port.",
						},
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Description: "Whether the rule is enabled.",
						},
						"position": schema.StringAttribute{
							Optional:    true,
							Description: "Rule position/order.",
						},
					},
				},
			},
			"destroy_firewall_rules_set": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "When true and the rules set was created by this resource, it will be deleted during destroy.",
				Default:     booldefault.StaticBool(true),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"firewall_rules_set_id": schema.StringAttribute{
				Computed:    true,
				Description: "Identifier of the firewall rules set created or referenced.",
			},
			"extra_volume_product": schema.StringAttribute{
				Optional:    true,
				Description: "Optional extra volume product slug to provision alongside the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"extra_volume_alias": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Alias assigned to the optional extra volume (defaults to hostname-extra when omitted).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"extra_volume_size": schema.Int64Attribute{
				Optional:    true,
				Description: "Size in GiB of the optional extra volume.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"destroy_extra_volume": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "When true (default) the extra volume is deleted during destroy; when false it is detached and kept.",
				Default:     booldefault.StaticBool(true),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"extra_volume_id": schema.StringAttribute{
				Computed:    true,
				Description: "Identifier of the optional extra volume.",
			},
			"no_network_acknowledged": schema.BoolAttribute{
				Optional:    true,
				Description: "Set true to confirm creation without a public network.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"wait_for_ready_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "Maximum seconds to wait for the server to reach running state (defaults to 300).",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current machine status reported by the API.",
			},
		},
	}
}

func (r *serverResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data Type", "Expected *Client for provider data")
		return
	}

	r.client = client
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Provider", "The provider must be configured before managing resources.")
		return
	}

	var plan serverResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !hasAuthentication(&plan) {
		resp.Diagnostics.AddError(
			"Missing Authentication Details",
			"At least one of password, ssh_pub_key, or ssh_pub_key_id must be supplied to authenticate with the server.",
		)
		return
	}

	firewallRulesCreate := boolValue(plan.FirewallRulesCreate, false)
	if !firewallRulesCreate && len(plan.FirewallRules) > 0 {
		resp.Diagnostics.AddError(
			"firewall_rules not supported",
			"firewall_rules can only be specified when firewall_rules_create is true.",
		)
		return
	}
	destroyFirewall := boolValue(plan.DestroyFirewallRulesSet, firewallRulesCreate)
	plan.FirewallRulesCreate = types.BoolValue(firewallRulesCreate)
	plan.DestroyFirewallRulesSet = types.BoolValue(destroyFirewall)

	if firewallRulesCreate {
		name := stringValue(plan.FirewallRulesSetName)
		if name == "" {
			resp.Diagnostics.AddAttributeError(
				path.Root("firewall_rules_set_name"),
				"Missing Firewall Rules Set Name",
				"When firewall_rules_create is true you must provide firewall_rules_set_name.",
			)
			return
		}

		firewall, err := r.client.API.CreateFirewallRulesSet(ctx, name)
		if err != nil {
			resp.Diagnostics.AddError("Error creating firewall rules set", err.Error())
			return
		}

		firewallID := stringFromAny(firewall["id"])
		firewallSlug := stringFromAny(firewall["slug"])
		if firewallSlug == "" {
			firewallSlug = name
		}

		plan.FirewallRulesSetID = types.StringValue(firewallID)
		plan.FwRulesSet = types.StringValue(firewallSlug)

		for idx, ruleModel := range plan.FirewallRules {
			ruleBody, diags := expandFirewallRule(ruleModel, idx)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			if len(ruleBody) == 0 {
				continue
			}
			if err := r.client.API.AddFirewallRule(ctx, firewallID, ruleBody); err != nil {
				resp.Diagnostics.AddError("Error adding firewall rule", err.Error())
				return
			}
		}
	} else {
		slug := stringValue(plan.FwRulesSet)
		identifier := stringValue(plan.FirewallRulesSetName)

		switch {
		case slug != "":
			firewall, err := r.client.API.FindFirewall(ctx, slug)
			if err != nil {
				if !errors.Is(err, errNotFound) {
					resp.Diagnostics.AddError("Firewall rules set not found", err.Error())
					return
				}
				resp.Diagnostics.AddAttributeError(
					path.Root("fw_rules_set"),
					"Firewall Rules Set Not Found",
					fmt.Sprintf("Firewall rules set %q does not exist. Set firewall_rules_create = true to create it or provide an existing slug.", slug),
				)
				return
			}
			firewallID := stringFromAny(firewall["id"])
			firewallSlug := stringFromAny(firewall["slug"])
			if firewallSlug == "" {
				firewallSlug = slug
			}
			plan.FirewallRulesSetID = types.StringValue(firewallID)
			plan.FwRulesSet = types.StringValue(firewallSlug)
		case identifier != "":
			firewall, err := r.client.API.FindFirewall(ctx, identifier)
			if err != nil {
				if !errors.Is(err, errNotFound) {
					resp.Diagnostics.AddError("Firewall rules set not found", err.Error())
					return
				}
				resp.Diagnostics.AddAttributeError(
					path.Root("firewall_rules_set_name"),
					"Firewall Rules Set Not Found",
					fmt.Sprintf("Firewall rules set %q does not exist. Set firewall_rules_create = true to create it or provide an existing name/slug.", identifier),
				)
				return
			}
			firewallID := stringFromAny(firewall["id"])
			firewallSlug := stringFromAny(firewall["slug"])
			if firewallSlug == "" {
				firewallSlug = identifier
			}
			plan.FirewallRulesSetID = types.StringValue(firewallID)
			plan.FwRulesSet = types.StringValue(firewallSlug)
		default:
			plan.FirewallRulesSetID = types.StringNull()
			plan.FwRulesSet = types.StringNull()
		}
	}

	wantsExtraVolume := !plan.ExtraVolumeProduct.IsNull() && !plan.ExtraVolumeProduct.IsUnknown()
	if wantsExtraVolume {
		if plan.ExtraVolumeSize.IsNull() || plan.ExtraVolumeSize.IsUnknown() {
			resp.Diagnostics.AddAttributeError(
				path.Root("extra_volume_size"),
				"Missing Extra Volume Size",
				"When extra_volume_product is set you must also provide extra_volume_size.",
			)
			return
		}
		if plan.ExtraVolumeSize.ValueInt64() <= 0 {
			resp.Diagnostics.AddAttributeError(
				path.Root("extra_volume_size"),
				"Invalid Extra Volume Size",
				"extra_volume_size must be greater than zero.",
			)
			return
		}
	}

	var fwRulesSetPtr *string
	if firewallRulesCreate || !plan.FirewallRulesSetID.IsNull() {
		fwRulesSetPtr = stringPointer(plan.FwRulesSet)
	}

	createRequest := ServerCreateRequest{
		Image:    plan.Image.ValueString(),
		Package:  plan.Package.ValueString(),
		Hostname: plan.Hostname.ValueString(),
		Project:  stringPointer(plan.Project),
		Password: stringPointer(plan.Password),
		SSHPubKey: func() *string {
			if plan.SSHPubKey.IsNull() || plan.SSHPubKey.IsUnknown() {
				return nil
			}
			value := plan.SSHPubKey.ValueString()
			return &value
		}(),
		SSHPubKeyID:           stringPointer(plan.SSHPubKeyID),
		PublicIP:              stringPointer(plan.PublicIP),
		NewIPv4:               boolPointer(plan.NewIPv4),
		PublicIPv6:            stringPointer(plan.PublicIPv6),
		NewIPv6:               boolPointer(plan.NewIPv6),
		FwRulesSet:            fwRulesSetPtr,
		FwPolicyIn:            stringPointer(plan.FwPolicyIn),
		FwPolicyOut:           stringPointer(plan.FwPolicyOut),
		PrivateNetwork:        stringPointer(plan.PrivateNetwork),
		PrivateAddress:        stringPointer(plan.PrivateAddress),
		NoNetworkAcknowledged: boolPointer(plan.NoNetworkAcknowledged),
	}

	server, err := r.client.API.CreateServer(ctx, createRequest)
	if err != nil {
		resp.Diagnostics.AddError("Error creating server", err.Error())
		return
	}

	waitSeconds := getWaitSeconds(plan.WaitForReadySeconds)
	readyServer := server
	if waitSeconds > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
		defer cancel()

		readyServer, err = r.client.API.WaitForServerReady(waitCtx, server.IDString(), 5*time.Second)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for server readiness", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(readyServer.IDString())
	plan.Status = types.StringValue(readyServer.EffectiveStatus())

	if wantsExtraVolume {
		extraAlias := plan.ExtraVolumeAlias.ValueString()
		if extraAlias == "" {
			extraAlias = fmt.Sprintf("%s-extra", plan.Hostname.ValueString())
		}

		volumeReq := VolumeCreateRequest{
			Alias:   extraAlias,
			Size:    plan.ExtraVolumeSize.ValueInt64(),
			Product: plan.ExtraVolumeProduct.ValueString(),
			Project: stringPointer(plan.Project),
		}

		volume, volErr := r.client.API.CreateServerVolume(ctx, plan.ID.ValueString(), volumeReq)
		if volErr != nil {
			resp.Diagnostics.AddError("Error creating extra volume", volErr.Error())
			return
		}

		plan.ExtraVolumeAlias = types.StringValue(volume.Alias)
		plan.ExtraVolumeID = types.StringValue(volume.IDString())
	} else {
		plan.ExtraVolumeAlias = types.StringNull()
		plan.ExtraVolumeID = types.StringNull()
	}

	plan.DestroyExtraVolume = types.BoolValue(boolValue(plan.DestroyExtraVolume, true))
	plan.DestroyPublicIPv4 = types.BoolValue(boolValue(plan.DestroyPublicIPv4, false))
	plan.DestroyPublicIPv6 = types.BoolValue(boolValue(plan.DestroyPublicIPv6, false))

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Provider", "The provider must be configured before managing resources.")
		return
	}

	var state serverResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.client.API.GetServer(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, errNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}

	state.Status = types.StringValue(server.EffectiveStatus())

	if !state.ExtraVolumeID.IsNull() && state.ExtraVolumeID.ValueString() != "" {
		volume, err := r.client.API.GetVolume(ctx, state.ExtraVolumeID.ValueString())
		if err != nil {
			if errors.Is(err, errNotFound) {
				state.ExtraVolumeID = types.StringNull()
				state.ExtraVolumeAlias = types.StringNull()
			} else {
				resp.Diagnostics.AddError("Error reading extra volume", err.Error())
				return
			}
		} else {
			state.ExtraVolumeAlias = types.StringValue(volume.Alias)
			state.ExtraVolumeID = types.StringValue(volume.IDString())
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Provider", "The provider must be configured before managing resources.")
		return
	}

	var plan serverResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state serverResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ID.ValueString()
	if serverID == "" {
		resp.Diagnostics.AddError("Missing Server Identifier", "State did not contain a server id to update.")
		return
	}

	waitSeconds := getWaitSeconds(plan.WaitForReadySeconds)
	destroyExtraVolume := boolValue(plan.DestroyExtraVolume, boolValue(state.DestroyExtraVolume, true))
	destroyPublicIPv4 := boolValue(plan.DestroyPublicIPv4, boolValue(state.DestroyPublicIPv4, false))
	destroyPublicIPv6 := boolValue(plan.DestroyPublicIPv6, boolValue(state.DestroyPublicIPv6, false))

	plan.ID = types.StringValue(serverID)
	if plan.FirewallRulesSetID.IsNull() || plan.FirewallRulesSetID.IsUnknown() {
		plan.FirewallRulesSetID = state.FirewallRulesSetID
	}
	if plan.FwRulesSet.IsNull() || plan.FwRulesSet.IsUnknown() {
		plan.FwRulesSet = state.FwRulesSet
	}
	if plan.FirewallRulesSetName.IsNull() || plan.FirewallRulesSetName.IsUnknown() {
		plan.FirewallRulesSetName = state.FirewallRulesSetName
	}
	if plan.FirewallRulesCreate.IsNull() || plan.FirewallRulesCreate.IsUnknown() {
		plan.FirewallRulesCreate = state.FirewallRulesCreate
	}
	if plan.DestroyFirewallRulesSet.IsNull() || plan.DestroyFirewallRulesSet.IsUnknown() {
		plan.DestroyFirewallRulesSet = state.DestroyFirewallRulesSet
	}

	detachVolume := func(volumeID, alias string) error {
		if volumeID == "" {
			return nil
		}

		if err := r.client.API.DetachVolume(ctx, volumeID, alias); err != nil && !errors.Is(err, errNotFound) {
			return err
		}

		if waitSeconds > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
			defer cancel()

			if err := r.client.API.WaitForVolumeDetached(waitCtx, volumeID, 5*time.Second); err != nil && !errors.Is(err, errNotFound) {
				return err
			}
		}

		return nil
	}

	deleteVolume := func(volumeID string) error {
		if volumeID == "" {
			return nil
		}

		if err := r.client.API.DeleteVolume(ctx, volumeID); err != nil && !errors.Is(err, errNotFound) {
			return err
		}

		if waitSeconds > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
			defer cancel()

			if err := r.client.API.WaitForVolumeDeletion(waitCtx, volumeID, 5*time.Second); err != nil && !errors.Is(err, errNotFound) {
				return err
			}
		}

		return nil
	}

	stateVolumeID := state.ExtraVolumeID.ValueString()
	stateVolumeAlias := state.ExtraVolumeAlias.ValueString()

	wantsExtraVolume := !plan.ExtraVolumeProduct.IsNull() && !plan.ExtraVolumeProduct.IsUnknown()

	var existingVolume *Volume
	if stateVolumeID != "" {
		volume, err := r.client.API.GetVolume(ctx, stateVolumeID)
		if err != nil {
			if !errors.Is(err, errNotFound) {
				resp.Diagnostics.AddError("Error reading extra volume", err.Error())
				return
			}
		} else {
			existingVolume = volume
		}
	}

	if wantsExtraVolume {
		if plan.ExtraVolumeSize.IsNull() || plan.ExtraVolumeSize.IsUnknown() {
			resp.Diagnostics.AddAttributeError(
				path.Root("extra_volume_size"),
				"Missing Extra Volume Size",
				"When extra_volume_product is set you must also provide extra_volume_size.",
			)
			return
		}
		if plan.ExtraVolumeSize.ValueInt64() <= 0 {
			resp.Diagnostics.AddAttributeError(
				path.Root("extra_volume_size"),
				"Invalid Extra Volume Size",
				"extra_volume_size must be greater than zero.",
			)
			return
		}

		desiredAlias := plan.ExtraVolumeAlias.ValueString()
		if plan.ExtraVolumeAlias.IsNull() || plan.ExtraVolumeAlias.IsUnknown() || desiredAlias == "" {
			desiredAlias = fmt.Sprintf("%s-extra", plan.Hostname.ValueString())
		}
		desiredProduct := plan.ExtraVolumeProduct.ValueString()
		if desiredProduct == "" {
			resp.Diagnostics.AddAttributeError(
				path.Root("extra_volume_product"),
				"Missing Extra Volume Product",
				"When extra_volume_product is set you must provide a storage product slug.",
			)
			return
		}
		desiredSize := plan.ExtraVolumeSize.ValueInt64()

		if existingVolume != nil {
			currentSize, err := existingVolume.Size.Int64()
			if err != nil {
				resp.Diagnostics.AddError("Error parsing extra volume size", err.Error())
				return
			}

			needRecreate := existingVolume.Product != desiredProduct
			updatePayload := VolumeUpdateRequest{}
			needsUpdate := false

			if existingVolume.Alias != desiredAlias {
				updatePayload.Alias = &desiredAlias
				needsUpdate = true
			}
			if currentSize != desiredSize {
				updatePayload.Size = &desiredSize
				needsUpdate = true
			}

			if needRecreate {
				if err := detachVolume(existingVolume.IDString(), existingVolume.Alias); err != nil {
					resp.Diagnostics.AddError("Error detaching extra volume", err.Error())
					return
				}
				if err := deleteVolume(existingVolume.IDString()); err != nil {
					resp.Diagnostics.AddError("Error deleting extra volume", err.Error())
					return
				}
				existingVolume = nil
			} else if needsUpdate {
				updatedVolume, err := r.client.API.UpdateVolume(ctx, existingVolume.IDString(), updatePayload)
				if err != nil {
					resp.Diagnostics.AddError("Error updating extra volume", err.Error())
					return
				}
				existingVolume = updatedVolume
			}
		}

		if existingVolume == nil {
			volumeReq := VolumeCreateRequest{
				Alias:   desiredAlias,
				Size:    desiredSize,
				Product: desiredProduct,
				Project: stringPointer(plan.Project),
			}

			volume, err := r.client.API.CreateServerVolume(ctx, serverID, volumeReq)
			if err != nil {
				resp.Diagnostics.AddError("Error creating extra volume", err.Error())
				return
			}

			existingVolume = volume
		}

		if existingVolume != nil {
			plan.ExtraVolumeAlias = types.StringValue(existingVolume.Alias)
			plan.ExtraVolumeID = types.StringValue(existingVolume.IDString())
		} else {
			plan.ExtraVolumeAlias = types.StringValue(desiredAlias)
			plan.ExtraVolumeID = types.StringNull()
		}
		plan.ExtraVolumeProduct = types.StringValue(desiredProduct)
		plan.ExtraVolumeSize = types.Int64Value(desiredSize)
	} else {
		if existingVolume != nil {
			alias := existingVolume.Alias
			if alias == "" {
				alias = stateVolumeAlias
			}

			if err := detachVolume(existingVolume.IDString(), alias); err != nil {
				resp.Diagnostics.AddError("Error detaching extra volume", err.Error())
				return
			}

			if destroyExtraVolume {
				if err := deleteVolume(existingVolume.IDString()); err != nil {
					resp.Diagnostics.AddError("Error deleting extra volume", err.Error())
					return
				}
			}
		}

		plan.ExtraVolumeAlias = types.StringNull()
		plan.ExtraVolumeID = types.StringNull()
		plan.ExtraVolumeSize = types.Int64Null()
		plan.ExtraVolumeProduct = types.StringNull()
	}

	plan.DestroyExtraVolume = types.BoolValue(destroyExtraVolume)
	plan.DestroyPublicIPv4 = types.BoolValue(destroyPublicIPv4)
	plan.DestroyPublicIPv6 = types.BoolValue(destroyPublicIPv6)

	server, err := r.client.API.GetServer(ctx, serverID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}

	plan.Status = types.StringValue(server.EffectiveStatus())

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Provider", "The provider must be configured before managing resources.")
		return
	}

	var state serverResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	waitSeconds := getWaitSeconds(state.WaitForReadySeconds)

	destroyExtraVolume := boolValue(state.DestroyExtraVolume, true)
	destroyFirewall := boolValue(state.DestroyFirewallRulesSet, boolValue(state.FirewallRulesCreate, false))
	destroyPublicIPv4 := boolValue(state.DestroyPublicIPv4, false)
	destroyPublicIPv6 := boolValue(state.DestroyPublicIPv6, false)
	volumeID := ""
	alias := ""
	if !state.ExtraVolumeID.IsNull() && state.ExtraVolumeID.ValueString() != "" {
		volumeID = state.ExtraVolumeID.ValueString()
		alias = state.ExtraVolumeAlias.ValueString()
	}

	firewallID := stringValue(state.FirewallRulesSetID)
	firewallIdentifier := stringValue(state.FwRulesSet)
	if firewallIdentifier == "" {
		firewallIdentifier = stringValue(state.FirewallRulesSetName)
	}

	serverID := state.ID.ValueString()
	serverHostname := stringValue(state.Hostname)

	cleanupPublicIPs := func(
		listFn func(context.Context) ([]map[string]any, error),
		detachFn func(context.Context, string) error,
		deleteFn func(context.Context, string) error,
		destroy bool,
		label string,
	) error {
		records, err := listFn(ctx)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return nil
			}
			return fmt.Errorf("listing %s addresses: %w", label, err)
		}
		for _, record := range records {
			if attached, ok := record["attached"].(bool); ok && !attached {
				continue
			}

			attachedTo := stringFromAny(record["server"])
			switch {
			case attachedTo == "":
				continue
			case serverID != "" && attachedTo == serverID:
				// match
			case serverHostname != "" && attachedTo == serverHostname:
				// match
			default:
				continue
			}

			ipID := stringFromAny(record["id"])
			if ipID == "" {
				continue
			}
			address := stringFromAny(record["address"])

			if err := detachFn(ctx, ipID); err != nil && !errors.Is(err, errNotFound) {
				return fmt.Errorf("detaching %s address %s: %w", label, address, err)
			}

			if destroy {
				if err := deleteFn(ctx, ipID); err != nil && !errors.Is(err, errNotFound) {
					return fmt.Errorf("deleting %s address %s: %w", label, address, err)
				}
			}
		}
		return nil
	}

	if !destroyExtraVolume && volumeID != "" {
		if err := r.client.API.DetachVolume(ctx, volumeID, alias); err != nil && !errors.Is(err, errNotFound) {
			resp.Diagnostics.AddError("Error detaching extra volume", err.Error())
			return
		}

		if waitSeconds > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
			if err := r.client.API.WaitForVolumeDetached(waitCtx, volumeID, 5*time.Second); err != nil && !errors.Is(err, errNotFound) {
				cancel()
				resp.Diagnostics.AddError("Error waiting for extra volume detach", err.Error())
				return
			}
			cancel()
		}
	}

	if err := cleanupPublicIPs(
		func(c context.Context) ([]map[string]any, error) { return r.client.API.ListIPs(c) },
		func(c context.Context, id string) error { return r.client.API.DetachIPv4(c, id) },
		func(c context.Context, id string) error { return r.client.API.DeleteIPv4(c, id) },
		destroyPublicIPv4,
		"IPv4",
	); err != nil {
		resp.Diagnostics.AddError("Error cleaning up IPv4 addresses", err.Error())
		return
	}

	if err := cleanupPublicIPs(
		func(c context.Context) ([]map[string]any, error) { return r.client.API.ListIPv6(c) },
		func(c context.Context, id string) error { return r.client.API.DetachIPv6(c, id) },
		func(c context.Context, id string) error { return r.client.API.DeleteIPv6(c, id) },
		destroyPublicIPv6,
		"IPv6",
	); err != nil {
		resp.Diagnostics.AddError("Error cleaning up IPv6 addresses", err.Error())
		return
	}

	if err := r.client.API.DeleteServer(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, errNotFound) {
			resp.Diagnostics.AddError("Error deleting server", err.Error())
			return
		}
	} else if waitSeconds > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
		defer cancel()
		if err := r.client.API.WaitForServerDeletion(waitCtx, state.ID.ValueString(), 5*time.Second); err != nil && !errors.Is(err, errNotFound) {
			resp.Diagnostics.AddError("Error waiting for server deletion", err.Error())
			return
		}
	}

	if destroyExtraVolume && volumeID != "" {
		if err := r.client.API.DeleteVolume(ctx, volumeID); err != nil && !errors.Is(err, errNotFound) {
			resp.Diagnostics.AddError("Error deleting extra volume", err.Error())
			return
		}

		if waitSeconds > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
			if err := r.client.API.WaitForVolumeDeletion(waitCtx, volumeID, 5*time.Second); err != nil && !errors.Is(err, errNotFound) {
				cancel()
				resp.Diagnostics.AddError("Error waiting for extra volume deletion", err.Error())
				return
			}
			cancel()
		}
	}

	if destroyFirewall {
		if firewallID == "" && firewallIdentifier != "" {
			firewall, err := r.client.API.FindFirewall(ctx, firewallIdentifier)
			if err != nil && !errors.Is(err, errNotFound) {
				resp.Diagnostics.AddError("Error locating firewall rules set", err.Error())
				return
			}
			if err == nil {
				firewallID = stringFromAny(firewall["id"])
			}
		}

		if firewallID != "" {
			if err := r.client.API.DeleteFirewallRulesSet(ctx, firewallID); err != nil && !errors.Is(err, errNotFound) {
				resp.Diagnostics.AddError("Error deleting firewall rules set", err.Error())
				return
			}
		}
	}

	resp.State.RemoveResource(ctx)
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueString()
	return &v
}

func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueBool()
	return &v
}

func boolValue(value types.Bool, defaultVal bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return defaultVal
	}
	return value.ValueBool()
}

func stringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func hasAuthentication(plan *serverResourceModel) bool {
	if plan == nil {
		return false
	}
	return !(plan.Password.IsNull() || plan.Password.IsUnknown()) ||
		!(plan.SSHPubKey.IsNull() || plan.SSHPubKey.IsUnknown()) ||
		!(plan.SSHPubKeyID.IsNull() || plan.SSHPubKeyID.IsUnknown())
}

func getWaitSeconds(value types.Int64) int64 {
	if value.IsNull() || value.IsUnknown() {
		return 300
	}
	v := value.ValueInt64()
	if v < 0 {
		return 0
	}
	return v
}

func expandFirewallRule(rule firewallRuleModel, _ int) (map[string]any, diag.Diagnostics) {
	body := make(map[string]any)

	if value := stringValue(rule.Direction); value != "" {
		body["direction"] = value
	}
	if value := stringValue(rule.Action); value != "" {
		body["action"] = value
	}
	if value := stringValue(rule.Protocol); value != "" {
		body["protocol"] = value
	}
	if value := stringValue(rule.Source); value != "" {
		body["source"] = value
	}
	if value := stringValue(rule.Sport); value != "" {
		body["sport"] = value
	}
	if value := stringValue(rule.Destination); value != "" {
		body["destination"] = value
	}
	if value := stringValue(rule.Dport); value != "" {
		body["dport"] = value
	}
	if !rule.Enabled.IsNull() && !rule.Enabled.IsUnknown() {
		body["enabled"] = rule.Enabled.ValueBool()
	}
	if value := stringValue(rule.Position); value != "" {
		body["position"] = value
	}

	return body, nil
}
