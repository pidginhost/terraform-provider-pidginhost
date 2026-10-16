package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &serverSnapshotResource{}
	_ resource.ResourceWithImportState = &serverSnapshotResource{}
)

const snapshotTimeout = 20 * time.Minute

// snapshotDeleting is the state of a snapshot whose deletion is in progress.
const snapshotDeleting = "delete"

func NewServerSnapshotResource() resource.Resource { return &serverSnapshotResource{} }

type serverSnapshotResource struct{ client *pidginhost.APIClient }

type serverSnapshotModel struct {
	ID            types.String   `tfsdk:"id"`
	ServerID      types.String   `tfsdk:"server_id"`
	Name          types.String   `tfsdk:"name"`
	Description   types.String   `tfsdk:"description"`
	IncludeMemory types.Bool     `tfsdk:"include_memory"`
	CreatedAt     types.String   `tfsdk:"created_at"`
	IsCurrent     types.Bool     `tfsdk:"is_current"`
	Timeouts      timeouts.Value `tfsdk:"timeouts"`
}

func (r *serverSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_snapshot"
}

func (r *serverSnapshotResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	id := idAttribute()
	id.Description = "Server id and snapshot name, as <server_id>/<name>."
	resp.Schema = schema.Schema{
		Description: "A snapshot of a cloud server. The API takes snapshots only while the server is active and not " +
			"booted from an ISO. Every argument is fixed at creation: changing one replaces the snapshot. The API " +
			"queues snapshot operations, so Terraform follows them in the server's activity log: it fails when the " +
			"server refuses the operation, and sends it again every 30 seconds while another operation holds the server. " +
			"Snapshots stay in state until deletion finishes.",
		Attributes: map[string]schema.Attribute{
			"id": id,
			"server_id": schema.StringAttribute{
				Required:      true,
				Description:   "Id of the server.",
				PlanModifiers: replace,
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Snapshot name, unique on the server: 2 to 40 letters, digits, _ or -, starting with a letter.",
				PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,39}$`),
					"must start with a letter and hold 2 to 40 letters, digits, _ or -")},
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(""),
				Description:   "Free text description, up to 200 characters.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthAtMost(200)},
			},
			"include_memory": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(false),
				Description:   "Whether to save the memory of the running server as well. Defaults to false.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "When the snapshot was taken.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"is_current": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether this is the server's current snapshot.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Delete: true}),
		},
	}
}

func (r *serverSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *serverSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	timeout, diags := plan.Timeouts.Create(ctx, snapshotTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	serverID, err := parseID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating server snapshot", err.Error())
		return
	}
	name := plan.Name.ValueString()
	body := pidginhost.NewSnapshotCreateRequest(name)
	if description := plan.Description.ValueString(); description != "" {
		body.Description = &description
	}
	body.IncludeMemory = plan.IncludeMemory.ValueBoolPointer()

	logs := serverActivity(ctx, r.client, serverID)
	entries, err := logs()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server activity", err))
		return
	}
	submit := func() error {
		_, _, err := r.client.CloudAPI.CloudServersSnapshotsCreate(ctx, serverID).SnapshotCreateRequest(*body).Execute()
		return err
	}
	if err := submit(); err != nil {
		resp.Diagnostics.Append(apiError("Creating server snapshot", err))
		return
	}
	// Keep the identity even if the wait fails, so the snapshot is not orphaned.
	plan.ID = types.StringValue(plan.ServerID.ValueString() + "/" + name)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), plan.ServerID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), plan.Name)...)

	var snapshot *pidginhost.Snapshot
	ready := func() (bool, error) {
		found, err := findSnapshot(ctx, r.client, serverID, name)
		snapshot = found
		return found != nil && found.State == "", err
	}
	prefix := fmt.Sprintf(`Snapshot "%s" `, name)
	refusals := []string{prefix + "could not be created", prefix + "was not created"}
	if err := waitActivity(ctx, logs, activityCounts(entries), refusals, submit, ready); err != nil {
		resp.Diagnostics.AddError("Waiting for server snapshot", describeError(err))
		return
	}
	plan.fill(snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, err := parseID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading server snapshot", err.Error())
		return
	}
	snapshot, err := findSnapshot(ctx, r.client, serverID, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server snapshot", err))
		return
	}
	if snapshot == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.fill(snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only stores new timeouts: every other argument replaces the snapshot.
func (r *serverSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Timeouts = plan.Timeouts
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *serverSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	timeout, diags := state.Timeouts.Delete(ctx, snapshotTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	serverID, err := parseID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Deleting server snapshot", err.Error())
		return
	}
	name := state.Name.ValueString()
	snapshot, err := findSnapshot(ctx, r.client, serverID, name)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server snapshot", err))
		return
	}
	if snapshot == nil {
		return
	}
	gone := func() (bool, error) {
		found, err := findSnapshot(ctx, r.client, serverID, name)
		return found == nil, err
	}
	if snapshot.State == snapshotDeleting {
		// A deletion already in progress only needs the wait.
		if err := waitFor(ctx, pollInterval, gone); err != nil {
			resp.Diagnostics.AddError("Waiting for server snapshot deletion", describeError(err))
		}
		return
	}

	logs := serverActivity(ctx, r.client, serverID)
	entries, err := logs()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server activity", err))
		return
	}
	submit := func() error {
		_, httpResp, err := r.client.CloudAPI.CloudServersSnapshotsDestroy(ctx, serverID, name).Execute()
		if isNotFound(httpResp, err) {
			return nil
		}
		return err
	}
	if err := submit(); err != nil {
		resp.Diagnostics.Append(apiError("Deleting server snapshot", err))
		return
	}
	prefix := fmt.Sprintf(`Snapshot "%s" `, name)
	refusals := []string{prefix + "could not be deleted", prefix + "was not deleted"}
	if err := waitActivity(ctx, logs, activityCounts(entries), refusals, submit, gone); err != nil {
		resp.Diagnostics.AddError("Waiting for server snapshot deletion", describeError(err))
	}
}

func (r *serverSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, name, ok := strings.Cut(req.ID, "/")
	if _, err := parseID(serverID); !ok || err != nil || name == "" {
		resp.Diagnostics.AddError("Importing server snapshot",
			fmt.Sprintf("Expected an id like <server_id>/<snapshot_name>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

func (m *serverSnapshotModel) fill(s *pidginhost.Snapshot) {
	m.ID = types.StringValue(m.ServerID.ValueString() + "/" + s.Name)
	m.Name = types.StringValue(s.Name)
	m.Description = types.StringValue(s.GetDescription())
	m.IncludeMemory = types.BoolValue(s.IncludesMemory)
	m.CreatedAt = types.StringPointerValue(s.CreatedAt.Get())
	m.IsCurrent = types.BoolValue(s.IsCurrent)
}

// findSnapshot returns the named snapshot of a server, or nil when the
// snapshot or the server is gone.
func findSnapshot(ctx context.Context, client *pidginhost.APIClient, serverID int32, name string) (*pidginhost.Snapshot, error) {
	snapshots, httpResp, err := client.CloudAPI.CloudServersSnapshotsList(ctx, serverID).Execute()
	if isNotFound(httpResp, err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i := range snapshots {
		if snapshots[i].Name == name {
			return &snapshots[i], nil
		}
	}
	return nil, nil
}
