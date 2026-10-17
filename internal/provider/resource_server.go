package provider

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure    = &serverResource{}
	_ resource.ResourceWithImportState  = &serverResource{}
	_ resource.ResourceWithUpgradeState = &serverResource{}
)

func NewServerResource() resource.Resource { return &serverResource{} }

type serverResource struct{ client *pidginhost.APIClient }

type serverModel struct {
	ID                    types.String   `tfsdk:"id"`
	Hostname              types.String   `tfsdk:"hostname"`
	Image                 types.String   `tfsdk:"image"`
	Package               types.String   `tfsdk:"package"`
	Generation            types.String   `tfsdk:"generation"`
	Project               types.String   `tfsdk:"project"`
	Password              types.String   `tfsdk:"password"`
	PasswordWO            types.String   `tfsdk:"password_wo"`
	PasswordWOVersion     types.Int64    `tfsdk:"password_wo_version"`
	SSHPubKey             types.String   `tfsdk:"ssh_pub_key"`
	SSHPubKeyID           types.String   `tfsdk:"ssh_pub_key_id"`
	UserData              types.String   `tfsdk:"user_data"`
	UserDataWO            types.String   `tfsdk:"user_data_wo"`
	UserDataWOVersion     types.Int64    `tfsdk:"user_data_wo_version"`
	PublicIP              types.String   `tfsdk:"public_ip"`
	NewIPv4               types.Bool     `tfsdk:"new_ipv4"`
	PublicIPv6            types.String   `tfsdk:"public_ipv6"`
	NewIPv6               types.Bool     `tfsdk:"new_ipv6"`
	NoNetworkAcknowledged types.Bool     `tfsdk:"no_network_acknowledged"`
	PrivateNetwork        types.String   `tfsdk:"private_network"`
	PrivateAddress        types.String   `tfsdk:"private_address"`
	EnableHA              types.Bool     `tfsdk:"enable_ha"`
	DestroyProtection     types.Bool     `tfsdk:"destroy_protection"`
	FwRulesSet            types.String   `tfsdk:"fw_rules_set"`
	FwPolicyIn            types.String   `tfsdk:"fw_policy_in"`
	FwPolicyOut           types.String   `tfsdk:"fw_policy_out"`
	DestroyPublicIPv4     types.Bool     `tfsdk:"destroy_public_ipv4"`
	DestroyPublicIPv6     types.Bool     `tfsdk:"destroy_public_ipv6"`
	Status                types.String   `tfsdk:"status"`
	IPv4Address           types.String   `tfsdk:"ipv4_address"`
	IPv6Address           types.String   `tfsdk:"ipv6_address"`
	CPUs                  types.Int64    `tfsdk:"cpus"`
	Memory                types.Int64    `tfsdk:"memory"`
	DiskSize              types.Int64    `tfsdk:"disk_size"`
	Username              types.String   `tfsdk:"username"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

// notNumeric refuses an id where the API echoes a slug or name back, which
// would otherwise show as a change on every plan.
func notNumeric(what string) validator.String {
	return stringvalidator.RegexMatches(regexp.MustCompile(`^$|[^0-9]`), "must be the "+what+", not its numeric id")
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *serverResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	createOnly := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:      true,
			Description:   desc + " Changing it replaces the server.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
	}
	createOnlyBool := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{
			Optional:      true,
			Description:   desc + " Changing it replaces the server.",
			PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
		}
	}
	computedString := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			Computed:    true,
			Description: desc,
		}
	}
	computedInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{
			Computed:    true,
			Description: desc,
		}
	}
	policy := func(direction string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:      true,
			Computed:      true,
			Description:   "Default firewall policy for " + direction + " traffic: ACCEPT, DROP or REJECT.",
			Validators:    []validator.String{stringvalidator.OneOf("ACCEPT", "DROP", "REJECT")},
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}

	resp.Schema = schema.Schema{
		Version:     1,
		Description: "A cloud server.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"hostname": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Hostname. The API picks one when left empty. Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"image": schema.StringAttribute{
				Required:      true,
				Description:   "Operating system image slug, from the pidginhost_images data source. Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"package": schema.StringAttribute{
				Required: true,
				Description: "Server package slug, from the pidginhost_packages data source. Changing it resizes the " +
					"server in place; the API refuses packages with a smaller disk.",
				Validators: []validator.String{notNumeric("package slug")},
			},
			"generation": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Hardware generation slug. The API picks the default when left empty. Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"project": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Project label.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Password for the default user. Changing it resets the password through cloud-init. " +
					"Unavailable on servers with a custom OS. Terraform keeps it in its state; password_wo does not.",
			},
			"password_wo": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				Description: "Password for the default user, never saved in the Terraform state. It is sent when the " +
					"server is created and when password_wo_version changes. Needs Terraform 1.11 or later.",
				Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("password"))},
			},
			"password_wo_version": schema.Int64Attribute{
				Optional: true,
				Description: "Change it to send the current password_wo to the server, which resets the password " +
					"through cloud-init.",
				Validators: []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("password_wo"))},
			},
			"ssh_pub_key": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Public key installed for SSH login. Changing it regenerates cloud-init and reboots a " +
					"running server. An empty string removes it from future cloud-init data but does not revoke keys " +
					"already in the guest.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ssh_pub_key_id": createOnly("Id or fingerprint of an SSH key stored on the account, installed at creation."),
			"user_data": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Startup script run on first boot: an executable script with a shebang. The API stores " +
					"it in cleartext. Changing it replaces the server, unless it moves to user_data_wo. Terraform " +
					"keeps it in its state; user_data_wo does not.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(userDataReplaces,
					"Changing it replaces the server, unless it moves to user_data_wo.",
					"Changing it replaces the server, unless it moves to `user_data_wo`.")},
			},
			"user_data_wo": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				Description: "Startup script run on first boot, never saved in the Terraform state. The API still " +
					"stores it in cleartext. It is sent only when the server is created. Needs Terraform 1.11 or later.",
				Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("user_data"))},
			},
			"user_data_wo_version": schema.Int64Attribute{
				Optional:    true,
				Description: "Setting or changing it replaces the server, which then boots with the current user_data_wo.",
				Validators:  []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("user_data_wo"))},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = !req.PlanValue.IsNull()
					},
					"Setting or changing it replaces the server.", "Setting or changing it replaces the server.")},
			},
			"public_ip":               createOnly("Id or address of an IPv4 address on the account to attach at creation."),
			"new_ipv4":                createOnlyBool("Allocate a new IPv4 address and attach it before first boot."),
			"public_ipv6":             createOnly("Id or address of an IPv6 address on the account to attach at creation."),
			"new_ipv6":                createOnlyBool("Allocate a new IPv6 address and attach it before first boot."),
			"no_network_acknowledged": createOnlyBool("Confirm creating a server without a public address."),
			"private_network":         createOnly("Id or address (CIDR) of a private network to join at creation."),
			"private_address":         createOnly("Address in private_network. The API assigns one when left empty."),
			"enable_ha": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Restart and move the server automatically when its host fails. Changing it replaces the server.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"destroy_protection": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Refuse to delete the server while true. Set it to false and apply before destroying.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"fw_rules_set": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Name of the firewall rule set applied to the public interface, for example " +
					"pidginhost_firewall.web.name. An empty string removes it. Some packages, such as the free tier, " +
					"keep their own firewall.",
				Validators:    []validator.String{notNumeric("rule set name")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fw_policy_in":  policy("incoming"),
			"fw_policy_out": policy("outgoing"),
			"destroy_public_ipv4": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Release the server's IPv4 addresses when it is destroyed. When false they stay on the account, unattached and billed.",
			},
			"destroy_public_ipv6": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Release the server's IPv6 addresses when it is destroyed. When false they stay on the account, unattached and billed.",
			},
			"status":       computedString("Status reported by the API, for example active."),
			"ipv4_address": computedString("Public IPv4 address, if any."),
			"ipv6_address": computedString("Public IPv6 address, if any."),
			"cpus":         computedInt("Number of virtual CPUs."),
			"memory":       computedInt("Memory in GiB."),
			"disk_size":    computedInt("Disk size in GiB."),
			"username":     computedString("Default user for SSH login."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 45*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var passwordWO, userDataWO types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("password_wo"), &passwordWO)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("user_data_wo"), &userDataWO)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := pidginhost.NewServerAddRequest(plan.Image.ValueString(), plan.Package.ValueString())
	body.Hostname = optionalString(plan.Hostname)
	body.Generation = optionalString(plan.Generation)
	body.Project = optionalString(plan.Project)
	body.Password = cmp.Or(optionalString(plan.Password), optionalString(passwordWO))
	body.SshPubKey = optionalString(plan.SSHPubKey)
	body.SshPubKeyId = optionalString(plan.SSHPubKeyID)
	body.UserData = cmp.Or(optionalString(plan.UserData), optionalString(userDataWO))
	body.PublicIp = optionalString(plan.PublicIP)
	body.NewIpv4 = optionalBool(plan.NewIPv4)
	body.PublicIpv6 = optionalString(plan.PublicIPv6)
	body.NewIpv6 = optionalBool(plan.NewIPv6)
	body.NoNetworkAcknowledged = optionalBool(plan.NoNetworkAcknowledged)
	body.PrivateNetwork = optionalString(plan.PrivateNetwork)
	body.PrivateAddress = optionalString(plan.PrivateAddress)
	body.EnableHa = optionalBool(plan.EnableHA)
	if v := optionalString(plan.FwRulesSet); v != nil && *v != "" {
		body.FwRulesSet = v
	}
	body.FwPolicyIn = policyValue(plan.FwPolicyIn)
	body.FwPolicyOut = policyValue(plan.FwPolicyOut)

	created, _, err := r.client.CloudAPI.CloudServersCreate(ctx).ServerAddRequest(*body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating server", err))
		return
	}
	// Saved before waiting: a server that fails to provision is still billed,
	// and Terraform must know it exists to replace or destroy it.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.Itoa(int(created.Id)))...)

	server, err := r.waitUntilActive(ctx, created.Id, nil)
	if err != nil {
		resp.Diagnostics.AddError("Waiting for the server to become active", err.Error())
		return
	}

	if plan.DestroyProtection.ValueBool() {
		if err := r.setDestroyProtection(ctx, created.Id, true); err != nil {
			resp.Diagnostics.Append(apiError("Enabling destroy protection", err))
			return
		}
		server.DestroyProtection = true
	}

	resp.Diagnostics.Append(r.refresh(ctx, &plan, server)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server id", err.Error())
		return
	}
	server, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) || err == nil && isGone(string(server.Status)) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server", err))
		return
	}
	resp.Diagnostics.Append(r.refresh(ctx, &state, server)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, 45*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server id", err.Error())
		return
	}

	if pkg := plan.Package.ValueString(); pkg != state.Package.ValueString() {
		_, _, err := r.client.CloudAPI.CloudServersModifyPackageCreate(ctx, id).
			ServerProductUpgradeRequest(*pidginhost.NewServerProductUpgradeRequest(pkg)).Execute()
		if err != nil {
			resp.Diagnostics.Append(apiError("Changing the server package", err))
			return
		}
		// The server goes through upgrading and back to active.
		_, err = r.waitUntilActive(ctx, id, func(s *pidginhost.ServerDetail) bool { return s.Package == pkg })
		if err != nil {
			resp.Diagnostics.AddError("Waiting for the package change", err.Error())
			return
		}
	}

	patch := pidginhost.PatchedServerDetailRequest{}
	changed := false
	if !plan.Project.IsUnknown() && !plan.Project.Equal(state.Project) {
		patch.Project, changed = optionalString(plan.Project), true
	}
	if !plan.Password.IsNull() && !plan.Password.Equal(state.Password) {
		patch.Password, changed = optionalString(plan.Password), true
	}
	if !plan.PasswordWOVersion.IsNull() && !plan.PasswordWOVersion.Equal(state.PasswordWOVersion) {
		var password types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("password_wo"), &password)...)
		if resp.Diagnostics.HasError() {
			return
		}
		patch.Password, changed = optionalString(password), true
	}
	if !plan.SSHPubKey.IsUnknown() && sshKeyBody(plan.SSHPubKey.ValueString()) != sshKeyBody(state.SSHPubKey.ValueString()) {
		patch.SshPubKey, changed = optionalString(plan.SSHPubKey), true
	}
	if changed {
		if _, _, err := r.client.CloudAPI.CloudServersPartialUpdate(ctx, id).PatchedServerDetailRequest(patch).Execute(); err != nil {
			resp.Diagnostics.Append(apiError("Updating server", err))
			return
		}
	}

	if !plan.DestroyProtection.IsUnknown() && !plan.DestroyProtection.Equal(state.DestroyProtection) {
		if err := r.setDestroyProtection(ctx, id, plan.DestroyProtection.ValueBool()); err != nil {
			resp.Diagnostics.Append(apiError("Changing destroy protection", err))
			return
		}
	}

	if firewallChanged(plan, state) {
		resp.Diagnostics.Append(r.applyFirewall(ctx, id, plan)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	server, _, err := r.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server", err))
		return
	}
	resp.Diagnostics.Append(r.refresh(ctx, &plan, server)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server id", err.Error())
		return
	}

	server, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server", err))
		return
	}
	if server.DestroyProtection {
		resp.Diagnostics.AddError("Destroy protection is on",
			fmt.Sprintf("Server %d has destroy_protection enabled. Set destroy_protection = false and apply before destroying it.", id))
		return
	}

	addresses, err := r.boundAddresses(ctx, id, state.DestroyPublicIPv4.ValueBool(), state.DestroyPublicIPv6.ValueBool())
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing the server's public addresses", err))
		return
	}

	// A server already being cancelled is not deleted twice.
	if status := string(server.Status); status != "cancelling" && status != "terminating" && !isGone(status) {
		httpResp, err := r.client.CloudAPI.CloudServersDestroy(ctx, id).Execute()
		if err != nil && !isNotFound(httpResp, err) {
			resp.Diagnostics.Append(apiError("Deleting server", err))
			return
		}
	}
	err = waitFor(ctx, pollInterval, func() (bool, error) {
		s, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
		if isNotFound(httpResp, err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return isGone(string(s.Status)), nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Waiting for the server to be deleted", describeError(err))
		return
	}

	// The server is gone, so failing here would only leave a state entry that
	// no longer knows these addresses. Warn instead, naming them.
	for _, address := range addresses {
		if err := address.release(ctx, r.client); err != nil {
			resp.Diagnostics.AddWarning("Public address "+address.address+" was not released",
				"The server is deleted but this address is still on the account and billed. Release it in the panel "+
					"or import it as pidginhost_public_ip and destroy it. Cause: "+describeError(err))
		}
	}
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// waitUntilActive polls a server until it is active and done() agrees. New
// servers answer 404 while pending, then move through provisioning to
// active or failed.
func (r *serverResource) waitUntilActive(ctx context.Context, id int32, done func(*pidginhost.ServerDetail) bool) (*pidginhost.ServerDetail, error) {
	var server *pidginhost.ServerDetail
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		s, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
		if isNotFound(httpResp, err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		server = s
		switch s.Status {
		case "failed":
			return false, fmt.Errorf("server %d failed to provision", id)
		case "cancelling", "cancelled", "terminating", "terminated":
			return false, fmt.Errorf("server %d cannot become active: status is %s", id, s.Status)
		case "active":
			return done == nil || done(s), nil
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s", describeError(err))
	}
	return server, nil
}

func (r *serverResource) setDestroyProtection(ctx context.Context, id int32, on bool) error {
	applied, _, err := r.client.CloudAPI.CloudServersDestroyProtectionCreate(ctx, id).
		DestroyProtectionRequest(*pidginhost.NewDestroyProtectionRequest(on)).Execute()
	if err != nil {
		return err
	}
	if applied.DestroyProtection != on {
		return fmt.Errorf("the API did not apply destroy_protection = %t for server %d", on, id)
	}
	return nil
}

func firewallChanged(plan, state serverModel) bool {
	changed := func(a, b types.String) bool { return !a.IsUnknown() && !a.Equal(b) }
	return changed(plan.FwRulesSet, state.FwRulesSet) || changed(plan.FwPolicyIn, state.FwPolicyIn) ||
		changed(plan.FwPolicyOut, state.FwPolicyOut)
}

// applyFirewall sends all three public interface settings, keeping the
// current value of any that is not configured.
func (r *serverResource) applyFirewall(ctx context.Context, id int32, plan serverModel) (diags diag.Diagnostics) {
	current, _, err := r.client.CloudAPI.CloudServersPublicInterfaceRetrieve(ctx, id).Execute()
	if err != nil {
		diags.Append(apiError("Reading the public interface (a server without a public address has no firewall)", err))
		return diags
	}
	body := pidginhost.PublicInterfaceRequest{FwPolicyIn: current.FwPolicyIn, FwPolicyOut: current.FwPolicyOut}
	body.FwRulesSet = current.FwRulesSet
	if !plan.FwRulesSet.IsUnknown() {
		if name := plan.FwRulesSet.ValueString(); name == "" {
			body.SetFwRulesSetNil()
		} else {
			body.SetFwRulesSet(name)
		}
	}
	if v := policyValue(plan.FwPolicyIn); v != nil {
		body.FwPolicyIn = v
	}
	if v := policyValue(plan.FwPolicyOut); v != nil {
		body.FwPolicyOut = v
	}
	applied, _, err := r.client.CloudAPI.CloudServersPublicInterfaceCreate(ctx, id).PublicInterfaceRequest(body).Execute()
	if err != nil {
		diags.Append(apiError("Updating the server firewall", err))
		return diags
	}
	// Some packages, such as the free tier, lock the firewall: the API
	// answers but keeps its own values.
	if applied.GetFwRulesSet() != body.GetFwRulesSet() || applied.GetFwPolicyIn() != body.GetFwPolicyIn() ||
		applied.GetFwPolicyOut() != body.GetFwPolicyOut() {
		diags.AddError("Firewall settings were not applied",
			"The API kept its own firewall settings for this server. Packages such as the free tier do not allow changing them.")
	}
	return diags
}

// refresh copies the API's view of a server into the model, keeping the
// configured spelling where the API stores the same value differently.
func (r *serverResource) refresh(ctx context.Context, m *serverModel, s *pidginhost.ServerDetail) (diags diag.Diagnostics) {
	m.ID = types.StringValue(strconv.Itoa(int(s.Id)))
	m.Hostname = types.StringValue(s.Hostname)
	if m.Image.IsNull() || m.Image.IsUnknown() {
		m.Image = types.StringValue(s.Image)
	}
	m.Package = types.StringValue(s.Package)
	m.Generation = types.StringValue(s.Generation)
	m.Project = types.StringValue(s.GetProject())
	if m.SSHPubKey.IsNull() || m.SSHPubKey.IsUnknown() || sshKeyBody(m.SSHPubKey.ValueString()) != sshKeyBody(s.GetSshPubKey()) {
		m.SSHPubKey = types.StringValue(s.GetSshPubKey())
	}
	m.EnableHA = types.BoolValue(s.HaEnabled)
	m.DestroyProtection = types.BoolValue(s.DestroyProtection)
	m.Status = types.StringValue(string(s.Status))
	m.IPv4Address = types.StringValue(s.Networks.Public.GetIpv4())
	m.IPv6Address = types.StringValue(s.Networks.Public.GetIpv6())
	m.CPUs = types.Int64Value(int64(s.Cpus))
	m.Memory = types.Int64Value(int64(s.Memory))
	m.DiskSize = types.Int64Value(int64(s.DiskSize))
	m.Username = types.StringValue(s.Username)
	if m.DestroyPublicIPv4.IsNull() || m.DestroyPublicIPv4.IsUnknown() {
		m.DestroyPublicIPv4 = types.BoolValue(false)
	}
	if m.DestroyPublicIPv6.IsNull() || m.DestroyPublicIPv6.IsUnknown() {
		m.DestroyPublicIPv6 = types.BoolValue(false)
	}

	iface, httpResp, err := r.client.CloudAPI.CloudServersPublicInterfaceRetrieve(ctx, s.Id).Execute()
	switch {
	case isNotFound(httpResp, err):
		// No public address, so no firewall.
		m.FwRulesSet = types.StringValue("")
		m.FwPolicyIn = types.StringNull()
		m.FwPolicyOut = types.StringNull()
	case err != nil:
		diags.Append(apiError("Reading the public interface", err))
	default:
		m.FwRulesSet = types.StringValue(iface.GetFwRulesSet())
		m.FwPolicyIn = types.StringValue(string(iface.GetFwPolicyIn()))
		m.FwPolicyOut = types.StringValue(string(iface.GetFwPolicyOut()))
	}
	return diags
}

// userDataReplaces replaces the server for a new startup script, but not when
// the same configuration moves the script to user_data_wo.
func userDataReplaces(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	var writeOnly types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("user_data_wo"), &writeOnly)...)
	resp.RequiresReplace = !req.PlanValue.IsNull() || writeOnly.IsNull()
}

func policyValue(v types.String) *pidginhost.FwPolicyOutEnum {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil
	}
	p := pidginhost.FwPolicyOutEnum(v.ValueString())
	return &p
}

// isGone reports a status the API keeps for deleted objects.
func isGone(status string) bool {
	return status == "cancelled" || status == "terminated"
}

// boundAddress is a public address to release after its server is gone.
type boundAddress struct {
	id      int32
	v6      bool
	address string
}

func (r *serverResource) boundAddresses(ctx context.Context, serverID int32, v4, v6 bool) ([]boundAddress, error) {
	var out []boundAddress
	if v4 {
		ips, err := fetchAll(func(page int32) ([]pidginhost.PublicIPv4, bool, error) {
			list, _, err := r.client.CloudAPI.CloudIpv4List(ctx).Page(page).Execute()
			if err != nil {
				return nil, false, err
			}
			return list.Results, list.GetNext() != "", nil
		})
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.ServerId.IsSet() && ip.ServerId.Get() != nil && *ip.ServerId.Get() == serverID {
				out = append(out, boundAddress{id: ip.Id, address: ip.Address})
			}
		}
	}
	if v6 {
		ips, err := fetchAll(func(page int32) ([]pidginhost.PublicIPv6, bool, error) {
			list, _, err := r.client.CloudAPI.CloudIpv6List(ctx).Page(page).Execute()
			if err != nil {
				return nil, false, err
			}
			return list.Results, list.GetNext() != "", nil
		})
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.ServerId.IsSet() && ip.ServerId.Get() != nil && *ip.ServerId.Get() == serverID {
				out = append(out, boundAddress{id: ip.Id, v6: true, address: ip.Address})
			}
		}
	}
	return out, nil
}

// release deletes an address once its deleted server let go of it.
func (a boundAddress) release(ctx context.Context, client *pidginhost.APIClient) error {
	return waitFor(ctx, pollInterval, func() (bool, error) {
		var httpResp *http.Response
		var err error
		if a.v6 {
			httpResp, err = client.CloudAPI.CloudIpv6Destroy(ctx, a.id).Execute()
		} else {
			httpResp, err = client.CloudAPI.CloudIpv4Destroy(ctx, a.id).Execute()
		}
		switch {
		case err == nil, isNotFound(httpResp, err):
			return true, nil
		case httpResp != nil && (httpResp.StatusCode == http.StatusBadRequest || httpResp.StatusCode == http.StatusConflict):
			// Still attached while the server's removal settles.
			return false, nil
		default:
			return false, err
		}
	})
}
