package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// serverModelV0 is pidginhost_server state written by provider 0.1.x. That
// version also created and deleted a firewall rule set and an extra volume
// for the server; those now have their own resources.
type serverModelV0 struct {
	ID                      types.String `tfsdk:"id"`
	Image                   types.String `tfsdk:"image"`
	Package                 types.String `tfsdk:"package"`
	Hostname                types.String `tfsdk:"hostname"`
	Project                 types.String `tfsdk:"project"`
	Password                types.String `tfsdk:"password"`
	SSHPubKey               types.String `tfsdk:"ssh_pub_key"`
	SSHPubKeyID             types.String `tfsdk:"ssh_pub_key_id"`
	PublicIP                types.String `tfsdk:"public_ip"`
	NewIPv4                 types.Bool   `tfsdk:"new_ipv4"`
	PublicIPv6              types.String `tfsdk:"public_ipv6"`
	NewIPv6                 types.Bool   `tfsdk:"new_ipv6"`
	DestroyPublicIPv4       types.Bool   `tfsdk:"destroy_public_ipv4"`
	DestroyPublicIPv6       types.Bool   `tfsdk:"destroy_public_ipv6"`
	FwRulesSet              types.String `tfsdk:"fw_rules_set"`
	FwPolicyIn              types.String `tfsdk:"fw_policy_in"`
	FwPolicyOut             types.String `tfsdk:"fw_policy_out"`
	PrivateNetwork          types.String `tfsdk:"private_network"`
	PrivateAddress          types.String `tfsdk:"private_address"`
	NoNetworkAcknowledged   types.Bool   `tfsdk:"no_network_acknowledged"`
	FirewallRulesCreate     types.Bool   `tfsdk:"firewall_rules_create"`
	FirewallRulesSetName    types.String `tfsdk:"firewall_rules_set_name"`
	FirewallRules           types.List   `tfsdk:"firewall_rules"`
	DestroyFirewallRulesSet types.Bool   `tfsdk:"destroy_firewall_rules_set"`
	FirewallRulesSetID      types.String `tfsdk:"firewall_rules_set_id"`
	ExtraVolumeProduct      types.String `tfsdk:"extra_volume_product"`
	ExtraVolumeAlias        types.String `tfsdk:"extra_volume_alias"`
	ExtraVolumeSize         types.Int64  `tfsdk:"extra_volume_size"`
	DestroyExtraVolume      types.Bool   `tfsdk:"destroy_extra_volume"`
	ExtraVolumeID           types.String `tfsdk:"extra_volume_id"`
	WaitForReadySeconds     types.Int64  `tfsdk:"wait_for_ready_seconds"`
	Status                  types.String `tfsdk:"status"`
}

func serverSchemaV0() *schema.Schema {
	str := func() schema.Attribute { return schema.StringAttribute{Optional: true, Computed: true} }
	boolean := func() schema.Attribute { return schema.BoolAttribute{Optional: true, Computed: true} }
	integer := func() schema.Attribute { return schema.Int64Attribute{Optional: true, Computed: true} }
	rule := map[string]schema.Attribute{}
	for _, name := range []string{"direction", "action", "protocol", "source", "sport", "destination", "dport", "position"} {
		rule[name] = schema.StringAttribute{Optional: true}
	}
	rule["enabled"] = schema.BoolAttribute{Optional: true}
	return &schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": str(), "image": str(), "package": str(), "hostname": str(), "project": str(),
			"password":       schema.StringAttribute{Optional: true, Sensitive: true},
			"ssh_pub_key":    schema.StringAttribute{Optional: true, Sensitive: true},
			"ssh_pub_key_id": str(), "public_ip": str(), "new_ipv4": boolean(), "public_ipv6": str(),
			"new_ipv6": boolean(), "destroy_public_ipv4": boolean(), "destroy_public_ipv6": boolean(),
			"fw_rules_set": str(), "fw_policy_in": str(), "fw_policy_out": str(), "private_network": str(),
			"private_address": str(), "no_network_acknowledged": boolean(), "firewall_rules_create": boolean(),
			"firewall_rules_set_name": str(),
			"firewall_rules": schema.ListNestedAttribute{
				Optional:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: rule},
			},
			"destroy_firewall_rules_set": boolean(), "firewall_rules_set_id": str(), "extra_volume_product": str(),
			"extra_volume_alias": str(), "extra_volume_size": integer(), "destroy_extra_volume": boolean(),
			"extra_volume_id": str(), "wait_for_ready_seconds": integer(), "status": str(),
		},
	}
}

func (r *serverResource) UpgradeState(_ context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: serverSchemaV0(),
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var old serverModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
				if resp.Diagnostics.HasError() {
					return
				}
				upgraded, notes := upgradeServerV0(old)
				for _, note := range notes {
					resp.Diagnostics.AddWarning("pidginhost_server no longer manages this object", note)
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, upgraded)...)
			},
		},
	}
}

// upgradeServerV0 maps 0.1.x state onto the current schema. Attributes the
// API reports are left for the refresh that follows to fill. The notes name
// the objects 0.1.x created for the server, which stay on the account and
// need importing into their own resources to stay under Terraform.
func upgradeServerV0(old serverModelV0) (serverModel, []string) {
	m := serverModel{
		ID:                    old.ID,
		Hostname:              old.Hostname,
		Image:                 old.Image,
		Package:               old.Package,
		Generation:            types.StringNull(),
		Project:               old.Project,
		Password:              old.Password,
		PasswordWO:            types.StringNull(),
		PasswordWOVersion:     types.Int64Null(),
		SSHPubKey:             old.SSHPubKey,
		SSHPubKeyID:           old.SSHPubKeyID,
		UserData:              types.StringNull(),
		UserDataWO:            types.StringNull(),
		UserDataWOVersion:     types.Int64Null(),
		PublicIP:              old.PublicIP,
		NewIPv4:               old.NewIPv4,
		PublicIPv6:            old.PublicIPv6,
		NewIPv6:               old.NewIPv6,
		NoNetworkAcknowledged: old.NoNetworkAcknowledged,
		PrivateNetwork:        old.PrivateNetwork,
		PrivateAddress:        old.PrivateAddress,
		EnableHA:              types.BoolNull(),
		DestroyProtection:     types.BoolNull(),
		FwRulesSet:            old.FwRulesSet,
		FwPolicyIn:            old.FwPolicyIn,
		FwPolicyOut:           old.FwPolicyOut,
		DestroyPublicIPv4:     boolOr(old.DestroyPublicIPv4, false),
		DestroyPublicIPv6:     boolOr(old.DestroyPublicIPv6, false),
		Status:                old.Status,
		IPv4Address:           types.StringNull(),
		IPv6Address:           types.StringNull(),
		CPUs:                  types.Int64Null(),
		Memory:                types.Int64Null(),
		DiskSize:              types.Int64Null(),
		Username:              types.StringNull(),
		Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
			"create": types.StringType, "update": types.StringType, "delete": types.StringType,
		})},
	}

	var notes []string
	if id := old.ExtraVolumeID.ValueString(); id != "" {
		notes = append(notes, fmt.Sprintf("The extra volume %q (id %s) stays attached to the server. "+
			"To keep managing it, import it: import { to = pidginhost_volume.extra  id = %q }",
			old.ExtraVolumeAlias.ValueString(), id, id))
	}
	if id := old.FirewallRulesSetID.ValueString(); id != "" && old.FirewallRulesCreate.ValueBool() {
		notes = append(notes, fmt.Sprintf("The firewall rule set %q (id %s) stays applied to the server. "+
			"To keep managing it, import it: import { to = pidginhost_firewall.server  id = %q }",
			old.FwRulesSet.ValueString(), id, id))
	}
	return m, notes
}

func boolOr(v types.Bool, fallback bool) types.Bool {
	if v.IsNull() || v.IsUnknown() {
		return types.BoolValue(fallback)
	}
	return v
}
