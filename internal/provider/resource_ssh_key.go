package provider

import (
	"context"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &sshKeyResource{}
	_ resource.ResourceWithImportState = &sshKeyResource{}
)

func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ client *pidginhost.APIClient }

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Alias       types.String `tfsdk:"alias"`
	Key         types.String `tfsdk:"key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An SSH public key stored on the account, for installing on new servers.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"alias": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Label shown for the key. The API picks one when left empty.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"key": schema.StringAttribute{
				Required:      true,
				Description:   "Public key in OpenSSH format. Changing it replaces the key.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"fingerprint": schema.StringAttribute{
				Computed:      true,
				Description:   "Fingerprint of the key.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := pidginhost.NewSSHKeyRequest(plan.Key.ValueString())
	body.Alias = optionalString(plan.Alias)
	key, _, err := r.client.AccountAPI.AccountSshKeysCreate(ctx).SSHKeyRequest(*body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating SSH key", err))
		return
	}
	plan.fill(key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, httpResp, err := r.client.AccountAPI.AccountSshKeysRetrieve(ctx, state.ID.ValueString()).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading SSH key", err))
		return
	}
	state.fill(key)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := pidginhost.PatchedSSHKeyUpdateRequest{Alias: optionalString(plan.Alias)}
	key, _, err := r.client.AccountAPI.AccountSshKeysPartialUpdate(ctx, plan.ID.ValueString()).PatchedSSHKeyUpdateRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Updating SSH key", err))
		return
	}
	plan.fill(key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	httpResp, err := r.client.AccountAPI.AccountSshKeysDestroy(ctx, state.ID.ValueString()).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting SSH key", err))
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *sshKeyModel) fill(key *pidginhost.SSHKey) {
	m.ID = types.StringValue(strconv.Itoa(int(key.Id)))
	m.Alias = types.StringValue(key.GetAlias())
	m.Fingerprint = types.StringValue(key.Fingerprint)
	// Keep the configured spelling while the API stores the same key, so a
	// trailing newline or a dropped comment does not show up as a change.
	if m.Key.IsNull() || sshKeyBody(m.Key.ValueString()) != sshKeyBody(key.Key) {
		m.Key = types.StringValue(key.Key)
	}
}

// sshKeyBody returns the type and base64 parts of an OpenSSH public key.
func sshKeyBody(key string) string {
	fields := strings.Fields(key)
	if len(fields) > 2 {
		fields = fields[:2]
	}
	return strings.Join(fields, " ")
}
