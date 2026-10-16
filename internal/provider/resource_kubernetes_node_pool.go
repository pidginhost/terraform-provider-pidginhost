package provider

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &kubernetesNodePoolResource{}
	_ resource.ResourceWithImportState = &kubernetesNodePoolResource{}
)

func NewKubernetesNodePoolResource() resource.Resource { return &kubernetesNodePoolResource{} }

type kubernetesNodePoolResource struct{ client *pidginhost.APIClient }

type kubernetesNodePoolModel struct {
	ID                    types.String   `tfsdk:"id"`
	ClusterID             types.String   `tfsdk:"cluster_id"`
	PoolID                types.String   `tfsdk:"pool_id"`
	Package               types.String   `tfsdk:"package"`
	Generation            types.String   `tfsdk:"generation"`
	Size                  types.Int64    `tfsdk:"size"`
	LocalDataLossAccepted types.Bool     `tfsdk:"local_data_loss_accepted"`
	Nodes                 types.List     `tfsdk:"nodes"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

var nodeAttrTypes = map[string]attr.Type{"id": types.StringType, "name": types.StringType, "ip": types.StringType}

func (r *kubernetesNodePoolResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_node_pool"
}

func (r *kubernetesNodePoolResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A pool of worker nodes in a Kubernetes cluster. A cluster holds at most one pool per package " +
			"and generation. The first pool, created with the cluster, can be imported and managed here too.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "<cluster_id>/<pool_id>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": clusterIDAttribute(),
			"pool_id": schema.StringAttribute{
				Computed:      true,
				Description:   "ID of the pool within its cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"package": schema.StringAttribute{
				Required:      true,
				Description:   "Worker package slug, from the pidginhost_kubernetes_cluster_types data source. Changing it replaces the pool.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"generation": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Hardware generation slug. The API picks the default when left empty. Changing it replaces the pool.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"size": schema.Int64Attribute{
				Required: true,
				Description: "Number of workers. It changes in place; shrinking drains the removed workers and needs " +
					"local_data_loss_accepted = true.",
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"local_data_loss_accepted": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Accept that removing workers destroys data kept on the nodes themselves (emptyDir and " +
					"local volumes). Shrinking the pool, or destroying it while it has workers, is refused without it.",
			},
			"nodes": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Workers in the pool.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":   schema.StringAttribute{Computed: true, Description: "Node ID."},
					"name": schema.StringAttribute{Computed: true, Description: "Node hostname."},
					"ip":   schema.StringAttribute{Computed: true, Description: "Node address, if assigned."},
				}},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *kubernetesNodePoolResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *kubernetesNodePoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesNodePoolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	clusterID, err := parseID(plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	if err := r.waitCluster(ctx, clusterID); err != nil {
		resp.Diagnostics.AddError("Waiting for the cluster", err.Error())
		return
	}
	body := pidginhost.ResourcePoolAddRequest{
		ResourcePoolPackage: plan.Package.ValueString(),
		ResourcePoolSize:    int32(plan.Size.ValueInt64()),
		Generation:          optionalString(plan.Generation),
	}
	created, _, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsCreate(ctx, clusterID).ResourcePoolAddRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating node pool", err))
		return
	}
	poolID := strconv.Itoa(int(created.Id))
	// Saved before waiting: the workers are billed even if the change fails.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ClusterID.ValueString()+"/"+poolID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), plan.ClusterID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("pool_id"), poolID)...)
	pool, err := r.settle(ctx, clusterID, poolID, plan.Size.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Creating node pool", err.Error())
		return
	}
	resp.Diagnostics.Append(plan.fill(pool)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesNodePoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesNodePoolModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := parseID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	pool, httpResp, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsRetrieve(ctx, clusterID, state.PoolID.ValueString()).Execute()
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading node pool", err))
		return
	}
	resp.Diagnostics.Append(state.fill(pool)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *kubernetesNodePoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state kubernetesNodePoolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	clusterID, err := parseID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	poolID := state.PoolID.ValueString()
	pool := (*pidginhost.ResourcePool)(nil)
	if want := plan.Size.ValueInt64(); want != state.Size.ValueInt64() {
		if pool, err = r.resize(ctx, clusterID, poolID, want, plan.LocalDataLossAccepted.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Resizing node pool", err.Error())
			return
		}
	} else if pool, _, err = r.client.KubernetesAPI.KubernetesClustersResourcePoolsRetrieve(ctx, clusterID, poolID).Execute(); err != nil {
		resp.Diagnostics.Append(apiError("Reading node pool", err))
		return
	}
	plan.ID, plan.PoolID = state.ID, state.PoolID
	resp.Diagnostics.Append(plan.fill(pool)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesNodePoolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesNodePoolModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	clusterID, err := parseID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	poolID := state.PoolID.ValueString()
	pool, httpResp, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsRetrieve(ctx, clusterID, poolID).Execute()
	if isNotFound(httpResp, err) {
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading node pool", err))
		return
	}
	if err := r.waitCluster(ctx, clusterID); err != nil {
		resp.Diagnostics.AddError("Waiting for the cluster", err.Error())
		return
	}
	// A pool with workers is removed through a journal; an empty one at once.
	after, err := r.lastJournal(ctx, clusterID)
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading removal journals", err))
		return
	}
	ack := state.LocalDataLossAccepted.ValueBool()
	httpResp, err = r.client.KubernetesAPI.KubernetesClustersResourcePoolsDestroy(ctx, clusterID, poolID).
		ResourcePoolDeleteRequest(pidginhost.ResourcePoolDeleteRequest{LocalDataLossAccepted: &ack}).Execute()
	if isNotFound(httpResp, err) {
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Deleting node pool", err))
		return
	}
	if len(pool.Nodes) == 0 {
		if err := r.waitCluster(ctx, clusterID); err != nil {
			resp.Diagnostics.AddError("Deleting node pool", err.Error())
		}
		return
	}
	if err := r.followJournal(ctx, clusterID, after, pool, nil); err != nil {
		resp.Diagnostics.AddError("Deleting node pool", err.Error())
	}
}

func (r *kubernetesNodePoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <cluster_id>/<pool_id>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("pool_id"), parts[1])...)
}

// resize changes the worker count. Growing waits for the cluster; shrinking
// follows the removal journal the API opens for the drained workers.
func (r *kubernetesNodePoolResource) resize(ctx context.Context, clusterID int32, poolID string, want int64, ack bool) (*pidginhost.ResourcePool, error) {
	if err := r.waitCluster(ctx, clusterID); err != nil {
		return nil, err
	}
	pool, _, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsRetrieve(ctx, clusterID, poolID).Execute()
	if err != nil {
		return nil, fmt.Errorf("%s", describeError(err))
	}
	shrink := want < int64(pool.Size)
	after, err := r.lastJournal(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("%s", describeError(err))
	}
	size := int32(want)
	body := pidginhost.PatchedResourcePoolRequest{NewSize: &size, LocalDataLossAccepted: &ack}
	if _, _, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsPartialUpdate(ctx, clusterID, poolID).PatchedResourcePoolRequest(body).Execute(); err != nil {
		return nil, fmt.Errorf("%s", describeError(err))
	}
	if shrink {
		if err := r.followJournal(ctx, clusterID, after, pool, &size); err != nil {
			return nil, err
		}
	}
	return r.settle(ctx, clusterID, poolID, want)
}

// settle waits for the cluster to finish a pool change and checks the pool
// reached the size asked for: a failed change still returns the cluster to
// active, so the worker count is what tells.
func (r *kubernetesNodePoolResource) settle(ctx context.Context, clusterID int32, poolID string, want int64) (*pidginhost.ResourcePool, error) {
	if err := r.waitCluster(ctx, clusterID); err != nil {
		return nil, err
	}
	pool, _, err := r.client.KubernetesAPI.KubernetesClustersResourcePoolsRetrieve(ctx, clusterID, poolID).Execute()
	if err != nil {
		return nil, fmt.Errorf("%s", describeError(err))
	}
	if int64(pool.Size) != want {
		return pool, fmt.Errorf("pool %s has %d workers instead of %d", poolID, pool.Size, want)
	}
	return pool, nil
}

// waitCluster waits until the cluster is active again after a change.
func (r *kubernetesNodePoolResource) waitCluster(ctx context.Context, clusterID int32) error {
	id := strconv.Itoa(int(clusterID))
	cluster, err := pollCluster(ctx, r.client, id, func(c *pidginhost.ClusterDetail) bool {
		return c != nil && (c.Status == "active" || c.Status == "failed" || isGone(string(c.Status)))
	})
	if err != nil {
		return err
	}
	if cluster == nil || cluster.Status != "active" {
		status := "gone"
		if cluster != nil {
			status = string(cluster.Status)
		}
		return fmt.Errorf("cluster %s is %s", id, status)
	}
	return nil
}

func (r *kubernetesNodePoolResource) journals(ctx context.Context, clusterID int32) ([]pidginhost.PoolRemovalJournal, error) {
	return fetchAll(func(page int32) ([]pidginhost.PoolRemovalJournal, bool, error) {
		list, _, err := r.client.KubernetesAPI.KubernetesClustersPoolRemovalJournalsList(ctx, clusterID).Page(page).Execute()
		if err != nil {
			return nil, false, err
		}
		return list.Results, list.GetNext() != "", nil
	})
}

func (r *kubernetesNodePoolResource) lastJournal(ctx context.Context, clusterID int32) (int32, error) {
	journals, err := r.journals(ctx, clusterID)
	last := int32(0)
	for _, j := range journals {
		last = max(last, j.Id)
	}
	return last, err
}

// followJournal waits for the journal the API opened for this removal and
// fails unless it succeeds. size is nil when the whole pool goes.
func (r *kubernetesNodePoolResource) followJournal(ctx context.Context, clusterID, after int32, pool *pidginhost.ResourcePool, size *int32) error {
	var journal *pidginhost.PoolRemovalJournal
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		journals, err := r.journals(ctx, clusterID)
		if err != nil {
			return false, err
		}
		journal = removalJournal(journals, after, pool, size)
		return journal != nil && slices.Contains(journalDone, string(journal.Status)), nil
	})
	if err != nil {
		if journal != nil {
			return fmt.Errorf("removing workers from pool %d: journal %d is %s: %s", pool.Id, journal.Id, journal.Status, journal.Message)
		}
		return fmt.Errorf("removing workers from pool %d: %s", pool.Id, describeError(err))
	}
	if journal.Status != "succeeded" {
		return fmt.Errorf("removing workers from pool %d is %s: %s", pool.Id, journal.Status, journal.Message)
	}
	return nil
}

// journalDone are the journal states the API stops in.
var journalDone = []string{"succeeded", "failed", "needs_attention"}

// removalJournal finds the first journal opened after `after` for this pool
// change: the right kind and requested size, removing exactly the expected
// number of the pool's own workers. The cluster can finish one change and
// start another between polls, so it keeps to the first match by id.
func removalJournal(journals []pidginhost.PoolRemovalJournal, after int32, pool *pidginhost.ResourcePool, size *int32) *pidginhost.PoolRemovalJournal {
	names := map[string]bool{}
	for _, n := range pool.Nodes {
		names[n.Name] = true
	}
	kind, removing := "pool_delete", len(names)
	if size != nil {
		kind, removing = "downsize", len(names)-int(*size)
	}
	sort.Slice(journals, func(i, j int) bool { return journals[i].Id < journals[j].Id })
	for i, j := range journals {
		requested := j.RequestedPoolSize.Get()
		if j.Id <= after || string(j.Kind) != kind || (size == nil) != (requested == nil) ||
			(size != nil && *requested != *size) || len(j.Items) != removing {
			continue
		}
		mine := true
		for _, item := range j.Items {
			mine = mine && names[item.TargetHostname]
		}
		if mine {
			return &journals[i]
		}
	}
	return nil
}

func (m *kubernetesNodePoolModel) fill(pool *pidginhost.ResourcePool) diag.Diagnostics {
	m.PoolID = types.StringValue(strconv.Itoa(int(pool.Id)))
	m.ID = types.StringValue(m.ClusterID.ValueString() + "/" + m.PoolID.ValueString())
	m.Package = types.StringValue(pool.Package)
	m.Generation = types.StringValue(pool.Generation)
	m.Size = types.Int64Value(int64(pool.Size))
	if m.LocalDataLossAccepted.IsNull() || m.LocalDataLossAccepted.IsUnknown() {
		m.LocalDataLossAccepted = types.BoolValue(false)
	}
	nodes := make([]attr.Value, len(pool.Nodes))
	for i, n := range pool.Nodes {
		ip := types.StringNull()
		if v := n.Ip.Get(); v != nil {
			ip = types.StringValue(*v)
		}
		nodes[i] = types.ObjectValueMust(nodeAttrTypes, map[string]attr.Value{
			"id": types.StringValue(strconv.Itoa(int(n.Id))), "name": types.StringValue(n.Name), "ip": ip,
		})
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: nodeAttrTypes}, nodes)
	m.Nodes = list
	return diags
}
