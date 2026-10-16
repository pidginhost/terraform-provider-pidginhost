package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
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

const kubernetesClusterServerTimeout = 20 * time.Minute

var (
	_ resource.ResourceWithConfigure   = &kubernetesClusterServerResource{}
	_ resource.ResourceWithImportState = &kubernetesClusterServerResource{}
)

func NewKubernetesClusterServerResource() resource.Resource {
	return &kubernetesClusterServerResource{}
}

type kubernetesClusterServerResource struct{ client *pidginhost.APIClient }

type kubernetesClusterServerModel struct {
	ID        types.String   `tfsdk:"id"`
	ClusterID types.String   `tfsdk:"cluster_id"`
	ServerID  types.String   `tfsdk:"server_id"`
	Hostname  types.String   `tfsdk:"hostname"`
	IP        types.String   `tfsdk:"ip"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

func (r *kubernetesClusterServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster_server"
}

func (r *kubernetesClusterServerResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Connects a cloud server to the private network of a Kubernetes cluster. Connecting or " +
			"disconnecting reboots the server. The server must be active, and the cluster must have cloud VM " +
			"access enabled in the control panel; the API cannot read that setting.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "<cluster_id>/<server_id>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": clusterIDAttribute(),
			"server_id": schema.StringAttribute{
				Required:      true,
				Description:   "ID of the cloud server. Changing it replaces the connection, rebooting both servers.",
				Validators:    []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]+$`), "must be a numeric ID")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"hostname": schema.StringAttribute{
				Computed:      true,
				Description:   "Host name of the server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ip": schema.StringAttribute{
				Computed:      true,
				Description:   "Address of the server in the cluster private network.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Delete: true}),
		},
	}
}

func (r *kubernetesClusterServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *kubernetesClusterServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesClusterServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, kubernetesClusterServerTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	clusterID := plan.ClusterID.ValueString()
	serverID, err := parseID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("server_id"), "Invalid server ID", err.Error())
		return
	}
	if err := connectServer(ctx, r.client, clusterID, serverID); err != nil {
		resp.Diagnostics.AddError("Connecting server to Kubernetes cluster", err.Error())
		return
	}
	plan.ID = types.StringValue(clusterID + "/" + plan.ServerID.ValueString())
	// Keep the identity even if the wait fails, so the connection is tainted, not lost.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), plan.ClusterID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), plan.ServerID)...)
	vm, err := waitServerConnection(ctx, r.client, clusterID, serverID, true)
	if err != nil {
		resp.Diagnostics.AddError("Connecting server to Kubernetes cluster", err.Error())
		return
	}
	plan.fill(vm)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesClusterServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesClusterServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, err := parseID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("server_id"), "Invalid server ID", err.Error())
		return
	}
	vm, err := connectedServer(ctx, r.client, state.ClusterID.ValueString(), serverID)
	if err != nil {
		resp.Diagnostics.AddError("Reading Kubernetes cluster server", err.Error())
		return
	}
	if vm == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.ClusterID.ValueString() + "/" + state.ServerID.ValueString())
	state.fill(vm)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only stores new timeouts; every other attribute replaces the connection.
func (r *kubernetesClusterServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kubernetesClusterServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesClusterServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesClusterServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, kubernetesClusterServerTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	serverID, err := parseID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("server_id"), "Invalid server ID", err.Error())
		return
	}
	if err := disconnectServer(ctx, r.client, state.ClusterID.ValueString(), serverID); err != nil {
		resp.Diagnostics.AddError("Disconnecting server from Kubernetes cluster", err.Error())
	}
}

func (r *kubernetesClusterServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <cluster_id>/<server_id>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), parts[1])...)
}

func (m *kubernetesClusterServerModel) fill(vm *pidginhost.ConnectedVM) {
	m.Hostname = types.StringValue(vm.Hostname)
	m.IP = types.StringValue(vm.Ip)
}

// connectedServer returns the server's entry among the servers connected to
// the cluster, or nil when it is not connected or the cluster is gone.
func connectedServer(ctx context.Context, client *pidginhost.APIClient, clusterID string, serverID int32) (*pidginhost.ConnectedVM, error) {
	list, httpResp, err := client.KubernetesAPI.KubernetesClustersConnectedVmsRetrieve(ctx, clusterID).Execute()
	if isNotFound(httpResp, err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New(describeError(err))
	}
	if i := slices.IndexFunc(list.Vms, func(vm pidginhost.ConnectedVM) bool { return vm.Id == serverID }); i >= 0 {
		return &list.Vms[i], nil
	}
	return nil, nil
}

// connectServer asks the API to connect the server, unless it already is:
// every connection reboots the server.
func connectServer(ctx context.Context, client *pidginhost.APIClient, clusterID string, serverID int32) error {
	if vm, err := connectedServer(ctx, client, clusterID, serverID); err != nil || vm != nil {
		return err
	}
	api := client.KubernetesAPI
	eligible, _, err := api.KubernetesClustersEligibleVmsRetrieve(ctx, clusterID).Execute()
	if err != nil {
		return errors.New(describeError(err))
	}
	if !slices.ContainsFunc(eligible.Vms, func(vm pidginhost.EligibleVM) bool { return vm.Id == serverID }) {
		return fmt.Errorf("server %d cannot be connected to cluster %s. Enable cloud VM access for the cluster "+
			"in the control panel, and make sure the server exists and is active", serverID, clusterID)
	}
	if _, _, err := api.KubernetesClustersConnectVmCreate(ctx, clusterID).ConnectVMRequest(pidginhost.ConnectVMRequest{Server: serverID}).Execute(); err != nil {
		return errors.New(describeError(err))
	}
	return nil
}

// disconnectServer disconnects the server and waits until the cluster no
// longer lists it.
func disconnectServer(ctx context.Context, client *pidginhost.APIClient, clusterID string, serverID int32) error {
	vm, err := connectedServer(ctx, client, clusterID, serverID)
	if err != nil || vm == nil {
		return err
	}
	_, httpResp, err := client.KubernetesAPI.KubernetesClustersDisconnectVmCreate(ctx, clusterID).
		DisconnectVMRequest(pidginhost.DisconnectVMRequest{Server: serverID}).Execute()
	if err != nil && !isNotFound(httpResp, err) {
		return errors.New(describeError(err))
	}
	_, err = waitServerConnection(ctx, client, clusterID, serverID, false)
	return err
}

// waitServerConnection waits until the cluster lists the server as connected,
// or no longer lists it.
func waitServerConnection(ctx context.Context, client *pidginhost.APIClient, clusterID string, serverID int32, connected bool) (*pidginhost.ConnectedVM, error) {
	var vm *pidginhost.ConnectedVM
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		var err error
		vm, err = connectedServer(ctx, client, clusterID, serverID)
		return (vm != nil) == connected, err
	})
	return vm, err
}
