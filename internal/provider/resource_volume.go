package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.ResourceWithConfigure   = &volumeResource{}
	_ resource.ResourceWithImportState = &volumeResource{}
	_ resource.ResourceWithModifyPlan  = &volumeResource{}
)

func NewVolumeResource() resource.Resource { return &volumeResource{} }

type volumeResource struct{ client *pidginhost.APIClient }

type volumeModel struct {
	ID       types.String   `tfsdk:"id"`
	ServerID types.String   `tfsdk:"server_id"`
	Alias    types.String   `tfsdk:"alias"`
	Size     types.Int64    `tfsdk:"size"`
	Product  types.String   `tfsdk:"product"`
	Project  types.String   `tfsdk:"project"`
	Status   types.String   `tfsdk:"status"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func (r *volumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (r *volumeResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A storage volume attached to a cloud server. If provisioning fails after allocation, " +
			"Terraform keeps the volume, attachment and configured timeouts in state for cleanup.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"server_id": schema.StringAttribute{
				Required: true,
				Description: "ID of the server the volume is attached to. Changing it detaches the volume and " +
					"attaches it to the new server without replacing it. Terraform waits until the target server " +
					"lists the attached volume.",
				Validators: []validator.String{numericID},
			},
			"alias": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Name of the volume. The API picks one when left empty.",
				Validators:    []validator.String{stringvalidator.LengthAtMost(50)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"size": schema.Int64Attribute{
				Required:    true,
				Description: "Size in GB. A volume can grow in place but cannot shrink.",
				Validators:  []validator.Int64{int64validator.AtLeast(1)},
			},
			"product": schema.StringAttribute{
				Required:      true,
				Description:   "Storage product slug or ID. Changing it replaces the volume.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"project": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Project name.",
				Validators:    []validator.String{stringvalidator.LengthAtMost(200)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed: true,
				Description: "Status reported by the API, such as active. Terraform waits for active after " +
					"creating, moving or growing the volume, and before growing it, since the API resizes only active volumes. " +
					"A failed, cancelled or terminated volume stops the wait with an error.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *volumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan refuses a smaller size before the apply, since the API cannot
// shrink a volume.
func (r *volumeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var before, after types.Int64
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("size"), &before)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("size"), &after)...)
	if after.IsUnknown() || after.ValueInt64() >= before.ValueInt64() {
		return
	}
	resp.Diagnostics.AddAttributeError(path.Root("size"), "Volumes cannot shrink",
		fmt.Sprintf("The volume is %d GB and cannot shrink to %d GB. Keep the size, or create a new, smaller volume.",
			before.ValueInt64(), after.ValueInt64()))
}

func (r *volumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan volumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := pidginhost.NewVolumeRequest(int32(plan.Size.ValueInt64()), plan.Product.ValueString())
	body.Alias = optionalString(plan.Alias)
	body.Project = optionalString(plan.Project)
	timeout, diags := plan.Timeouts.Create(ctx, computeDefaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	v, _, err := r.client.CloudAPI.CloudServersVolumesCreate(ctx, plan.ServerID.ValueString()).VolumeRequest(*body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating volume", err))
		return
	}
	// Saved before waiting, so a volume that fails to provision is tainted, not orphaned.
	// Keep its attachment and configured timeouts for the cleanup as well.
	plan.fill(v)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if v, err = r.waitVolume(ctx, v.Id, volumeActive); err == nil && v == nil {
		err = fmt.Errorf("the volume disappeared while it was being created")
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Waiting for the volume to become active", err))
		return
	}
	plan.fill(v)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *volumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state volumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid volume id", err.Error())
		return
	}
	v, httpResp, err := r.client.CloudAPI.CloudVolumesRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading volume", err))
		return
	}
	serverID, err := r.serverOf(ctx, v, state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.Append(apiError("Finding the server of the volume", err))
		return
	}
	state.fill(v)
	state.ServerID = serverID
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *volumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state volumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid volume id", err.Error())
		return
	}

	timeout, diags := plan.Timeouts.Update(ctx, computeDefaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var v *pidginhost.Volume
	if !plan.ServerID.Equal(state.ServerID) {
		if v, err = r.move(ctx, id, plan.ServerID.ValueString()); err != nil {
			resp.Diagnostics.Append(apiError("Moving volume", err))
			return
		}
	}

	var body pidginhost.PatchedVolumeUpdateRequest
	if !plan.Alias.Equal(state.Alias) {
		body.Alias = optionalString(plan.Alias)
	}
	if !plan.Project.Equal(state.Project) {
		body.Project = optionalString(plan.Project)
	}
	grow := !plan.Size.Equal(state.Size)
	if grow {
		body.Size = optionalInt32(plan.Size)
		// The API resizes only active volumes; a new or moved one may still be busy.
		if _, err = r.waitVolume(ctx, id, volumeActive); err != nil {
			resp.Diagnostics.Append(apiError("Waiting for the volume to become active", err))
			return
		}
	}
	if body.Alias != nil || body.Size != nil || body.Project != nil {
		if v, _, err = r.client.CloudAPI.CloudVolumesPartialUpdate(ctx, id).PatchedVolumeUpdateRequest(body).Execute(); err != nil {
			resp.Diagnostics.Append(apiError("Updating volume", err))
			return
		}
	}
	if grow {
		want := plan.Size.ValueInt64()
		v, err = r.waitVolume(ctx, id, func(v *pidginhost.Volume) (bool, error) {
			active, err := volumeActive(v)
			return active && int64(v.Size) == want, err
		})
		if err != nil {
			resp.Diagnostics.Append(apiError("Waiting for the resize", err))
			return
		}
	}

	if v == nil {
		if v, _, err = r.client.CloudAPI.CloudVolumesRetrieve(ctx, id).Execute(); err != nil {
			resp.Diagnostics.Append(apiError("Reading volume", err))
			return
		}
	}
	plan.fill(v)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *volumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state volumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid volume id", err.Error())
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, computeDefaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// A volume still provisioning or changing cannot be cancelled yet.
	if _, err := r.waitVolume(waitCtx, id, func(v *pidginhost.Volume) (bool, error) { return !volumeBusy(v), nil }); err != nil {
		resp.Diagnostics.Append(apiError("Waiting for the volume to settle", err))
		return
	}
	// The API refuses to delete a volume while it is on a server.
	v, err := r.detach(waitCtx, id)
	if err != nil {
		resp.Diagnostics.Append(apiError("Detaching volume", err))
		return
	}
	if v == nil {
		return
	}
	httpResp, err := r.client.CloudAPI.CloudVolumesDestroy(ctx, id).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting volume", err))
	}
}

func (r *volumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// move detaches the volume, attaches it to serverID and waits until the
// target server lists the attached volume.
func (r *volumeResource) move(ctx context.Context, id int32, serverID string) (*pidginhost.Volume, error) {
	vm, err := parseID(serverID)
	if err != nil {
		return nil, err
	}
	v, err := r.detach(ctx, id)
	if err == nil && v == nil {
		err = fmt.Errorf("volume %d no longer exists", id)
	}
	if err != nil {
		return nil, err
	}
	if _, _, err := r.client.CloudAPI.CloudVolumesAttachCreate(ctx, id).AttachVolumeRequest(*pidginhost.NewAttachVolumeRequest(vm)).Execute(); err != nil {
		return nil, err
	}
	v, err = r.waitVolume(ctx, id, func(v *pidginhost.Volume) (bool, error) {
		if active, err := volumeActive(v); !active || err != nil || !v.Attached || v.Server == "" {
			return false, err
		}
		return r.serverHasVolume(ctx, vm, id)
	})
	if err == nil && v == nil {
		err = fmt.Errorf("volume %d no longer exists", id)
	}
	return v, err
}

// detach takes the volume off its server and waits until the API drops the
// server link, which happens some time after the detach call returns. It
// returns nil when the volume no longer exists.
func (r *volumeResource) detach(ctx context.Context, id int32) (*pidginhost.Volume, error) {
	v, httpResp, err := r.client.CloudAPI.CloudVolumesRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		return nil, nil
	}
	if err != nil || v.Server == "" {
		return v, err
	}
	// A volume that names a server but is no longer attached is detaching.
	if v.Attached {
		if _, _, err := r.client.CloudAPI.CloudVolumesDetachCreate(ctx, id).Execute(); err != nil {
			return nil, err
		}
	}
	return r.waitVolume(ctx, id, func(v *pidginhost.Volume) (bool, error) { return v.Server == "", nil })
}

// waitVolume polls the volume until done holds. It returns nil when the
// volume no longer exists.
func (r *volumeResource) waitVolume(ctx context.Context, id int32, done func(*pidginhost.Volume) (bool, error)) (*pidginhost.Volume, error) {
	var v *pidginhost.Volume
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		got, httpResp, err := r.client.CloudAPI.CloudVolumesRetrieve(ctx, id).Execute()
		if isNotFound(httpResp, err) {
			v = nil
			return true, nil
		}
		if err != nil {
			return false, err
		}
		v = got
		return done(got)
	})
	return v, err
}

// serverOf returns the id of the server the volume is on. The volume only
// names the server's hostname, which servers can share, so it looks for the
// volume in the servers' own volume lists, starting with the known server.
func (r *volumeResource) serverOf(ctx context.Context, v *pidginhost.Volume, known string) (types.String, error) {
	if v.Server == "" {
		return types.StringNull(), nil
	}
	if id, err := parseID(known); err == nil {
		found, err := r.serverHasVolume(ctx, id, v.Id)
		if err != nil {
			return types.StringNull(), err
		}
		if found {
			return types.StringValue(known), nil
		}
	}
	servers, err := fetchAll(func(page int32) ([]pidginhost.Server, bool, error) {
		list, _, err := r.client.CloudAPI.CloudServersList(ctx).Page(page).Execute()
		if err != nil {
			return nil, false, err
		}
		return list.Results, list.GetNext() != "", nil
	})
	if err != nil {
		return types.StringNull(), err
	}
	for _, s := range servers {
		id := strconv.Itoa(int(s.Id))
		if s.GetHostname() != v.Server || id == known {
			continue
		}
		found, err := r.serverHasVolume(ctx, s.Id, v.Id)
		if err != nil {
			return types.StringNull(), err
		}
		if found {
			return types.StringValue(id), nil
		}
	}
	return types.StringNull(), nil
}

func (r *volumeResource) serverHasVolume(ctx context.Context, serverID, volumeID int32) (bool, error) {
	s, httpResp, err := r.client.CloudAPI.CloudServersRetrieve(ctx, serverID).Execute()
	if isNotFound(httpResp, err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(s.Volumes, func(v pidginhost.Volume) bool { return v.Id == volumeID }), nil
}

func (m *volumeModel) fill(v *pidginhost.Volume) {
	m.ID = types.StringValue(strconv.Itoa(int(v.Id)))
	m.Alias = types.StringValue(v.GetAlias())
	m.Size = types.Int64Value(int64(v.Size))
	m.Project = types.StringValue(v.GetProject())
	m.Status = types.StringValue(string(v.Status))
	// The API takes a product ID or slug; keep the one configured.
	if m.Product.IsNull() || m.Product.IsUnknown() {
		m.Product = types.StringValue(v.Product)
	}
}

// volumeActive reports whether the API can change the volume now. A failed
// or deleted volume will not become active by waiting.
func volumeActive(v *pidginhost.Volume) (bool, error) {
	if v.Status == pidginhost.RESOURCESTATUSENUM_FAILED || isGone(string(v.Status)) {
		return false, fmt.Errorf("volume %d is %s", v.Id, v.Status)
	}
	return v.Status == pidginhost.RESOURCESTATUSENUM_ACTIVE, nil
}

// volumeBusy reports a volume the API is still provisioning or changing.
func volumeBusy(v *pidginhost.Volume) bool {
	switch v.Status {
	case pidginhost.RESOURCESTATUSENUM_PENDING, pidginhost.RESOURCESTATUSENUM_PROVISIONING, pidginhost.RESOURCESTATUSENUM_UPGRADING:
		return true
	}
	return false
}
