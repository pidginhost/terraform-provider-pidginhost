package provider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

const kubernetesClusterTimeout = 90 * time.Minute

var (
	_ resource.ResourceWithConfigure   = &kubernetesClusterResource{}
	_ resource.ResourceWithImportState = &kubernetesClusterResource{}
	_ resource.ResourceWithModifyPlan  = &kubernetesClusterResource{}
)

func NewKubernetesClusterResource() resource.Resource { return &kubernetesClusterResource{} }

type kubernetesClusterResource struct{ client *pidginhost.APIClient }

type kubernetesClusterModel struct {
	ID                         types.String   `tfsdk:"id"`
	Name                       types.String   `tfsdk:"name"`
	ClusterType                types.String   `tfsdk:"cluster_type"`
	ResourcePoolPackage        types.String   `tfsdk:"resource_pool_package"`
	ResourcePoolSize           types.Int64    `tfsdk:"resource_pool_size"`
	Generation                 types.String   `tfsdk:"generation"`
	EnableGatewayAPI           types.Bool     `tfsdk:"enable_gateway_api"`
	DualStack                  types.Bool     `tfsdk:"dual_stack"`
	KubeVersion                types.String   `tfsdk:"kube_version"`
	Features                   types.Set      `tfsdk:"features"`
	Protected                  types.Bool     `tfsdk:"protected"`
	Encryption                 types.String   `tfsdk:"encryption"`
	AcknowledgeWorkloadRestart types.Bool     `tfsdk:"acknowledge_workload_restart"`
	Status                     types.String   `tfsdk:"status"`
	TalosVersion               types.String   `tfsdk:"talos_version"`
	KubeconfigValidUntil       types.String   `tfsdk:"kubeconfig_valid_until"`
	IPv4Address                types.String   `tfsdk:"ipv4_address"`
	IPv6Address                types.String   `tfsdk:"ipv6_address"`
	Timeouts                   timeouts.Value `tfsdk:"timeouts"`
}

func (r *kubernetesClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster"
}

func (r *kubernetesClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	features := kubernetesFeatures()
	resp.Schema = schema.Schema{
		Description: "A managed Kubernetes cluster. Provisioning a cluster can take more than 30 minutes.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"name": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Cluster name. The API picks one when left empty.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_type": schema.StringAttribute{
				Required:      true,
				Description:   "Cluster type: dev, prod or beta. New prod clusters start protected. Changing it replaces the cluster.",
				Validators:    []validator.String{stringvalidator.OneOf("dev", "prod", "beta")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"resource_pool_package": schema.StringAttribute{
				Required:      true,
				Description:   "ID or slug of the worker package of the first resource pool. Only used when the cluster is created.",
				PlanModifiers: []planmodifier.String{firstPoolString{}},
			},
			"resource_pool_size": schema.Int64Attribute{
				Optional:      true,
				Description:   "Number of workers in the first resource pool, the cluster type minimum when not set. Only used when the cluster is created. This provider cannot resize pools; use the control panel.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{firstPoolInt64{}},
			},
			"generation": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Hardware generation slug. Changing it replaces the cluster.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enable_gateway_api": schema.BoolAttribute{
				Optional:      true,
				Description:   "Enable the Gateway API. The API enables it when not set. Changing, setting or removing it replaces the cluster. The API does not return this setting; leave it unset after import to keep the existing cluster.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"dual_stack": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "IPv6 dual stack for pods, services and the cluster private network. The API ignores it where " +
					"the platform does not offer dual stack. Changing it replaces the cluster.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplace(),
				},
			},
			"kube_version": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Kubernetes version, for example 1.35.7. The latest offered version when not set. Raising it " +
					"upgrades the cluster one version at a time. Downgrades are not possible.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"features": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Description: "Optional cluster features: " + strings.Join(features, ", ") +
					". They install in the background after a change.",
				Validators:    []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf(features...))},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"protected": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Protect the cluster from deletion. To destroy a protected cluster, set it to false and " +
					"apply first.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"encryption": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "WireGuard encryption of pod traffic: disabled or enabled. Changing it restarts the " +
					"workloads and requires acknowledge_workload_restart = true.",
				Validators:    []validator.String{stringvalidator.OneOf("disabled", "enabled")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"acknowledge_workload_restart": schema.BoolAttribute{
				Optional:    true,
				Description: "Accept that changing encryption restarts the workloads of the cluster.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster status, such as active, upgrading or failed.",
			},
			"talos_version": schema.StringAttribute{
				Computed:    true,
				Description: "Talos Linux version of the cluster nodes.",
			},
			"kubeconfig_valid_until": schema.StringAttribute{
				Computed:    true,
				Description: "Time the current kubeconfig expires.",
			},
			"ipv4_address": schema.StringAttribute{
				Computed:      true,
				Description:   "Public IPv4 address of the cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ipv6_address": schema.StringAttribute{
				Computed:      true,
				Description:   "Public IPv6 address of the cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *kubernetesClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan refuses an encryption change without the workload restart
// acknowledgement at plan time, before any other change is applied.
func (r *kubernetesClusterResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var want types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("encryption"), &want)...)
	if want.IsNull() || want.IsUnknown() {
		return
	}
	// New clusters start without encryption.
	prior := types.StringValue("disabled")
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("encryption"), &prior)...)
	}
	if want.Equal(prior) {
		return
	}
	var ack types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("acknowledge_workload_restart"), &ack)...)
	if ack.IsUnknown() || ack.ValueBool() {
		return
	}
	resp.Diagnostics.AddAttributeError(path.Root("encryption"), "Workload restart not acknowledged",
		"Changing encryption restarts the workloads of the cluster. Set acknowledge_workload_restart = true to accept it.")
}

func (r *kubernetesClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, kubernetesClusterTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A literal, not NewClusterAddRequest: the constructor fills in a fixed
	// kube_version instead of letting the API pick the latest.
	body := pidginhost.ClusterAddRequest{
		ClusterType:         pidginhost.ClusterTypeEnum(plan.ClusterType.ValueString()),
		ResourcePoolPackage: plan.ResourcePoolPackage.ValueString(),
		Name:                optionalString(plan.Name),
		ResourcePoolSize:    optionalInt32(plan.ResourcePoolSize),
		Generation:          optionalString(plan.Generation),
		EnableGatewayApi:    optionalBool(plan.EnableGatewayAPI),
		DualStack:           optionalBool(plan.DualStack),
	}
	if v := optionalString(plan.KubeVersion); v != nil {
		version := pidginhost.KubeVersionEnum(*v)
		body.KubeVersion = &version
	}
	if !plan.Features.IsNull() && !plan.Features.IsUnknown() {
		body.Features = clusterFeatures(ctx, plan.Features, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	created, _, err := r.client.KubernetesAPI.KubernetesClustersCreate(ctx).ClusterAddRequest(body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating Kubernetes cluster", err))
		return
	}
	id := strconv.Itoa(int(created.Id))
	// Keep the id even if the wait fails, so the cluster is tainted, not orphaned.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)

	if err := r.provision(ctx, id, plan); err != nil {
		resp.Diagnostics.AddError("Creating Kubernetes cluster", fmt.Sprintf("Cluster %s: %s", id, err))
		return
	}
	plan.ID = types.StringValue(id)
	if r.reload(ctx, &plan, &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

// provision waits for a new cluster and applies the settings the API does
// not take on creation.
func (r *kubernetesClusterResource) provision(ctx context.Context, id string, plan kubernetesClusterModel) error {
	// New clusters answer 404 while pending, then move through provisioning to active or failed.
	cluster, err := pollCluster(ctx, r.client, id, func(c *pidginhost.ClusterDetail) bool {
		return c != nil && (c.Status == pidginhost.RESOURCESTATUSENUM_ACTIVE || c.Status == pidginhost.RESOURCESTATUSENUM_FAILED)
	})
	if err == nil {
		err = requireActive(cluster)
	}
	if err != nil {
		return err
	}
	// Protection is not a creation field, and prod clusters start protected.
	if want := optionalBool(plan.Protected); want != nil && *want != cluster.GetProtected() {
		body := pidginhost.PatchedClusterDetailRequest{Protected: want}
		if _, _, err := r.client.KubernetesAPI.KubernetesClustersPartialUpdate(ctx, id).PatchedClusterDetailRequest(body).Execute(); err != nil {
			return errors.New(describeError(err))
		}
	}
	if want := optionalString(plan.Encryption); want != nil {
		return setEncryption(ctx, r.client, id, encryptionMode(*want), plan.AcknowledgeWorkloadRestart.ValueBool())
	}
	return nil
}

func (r *kubernetesClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.load(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *kubernetesClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state kubernetesClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, kubernetesClusterTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := state.ID.ValueString()
	if err := r.update(ctx, id, plan, state, &resp.Diagnostics); err != nil {
		resp.Diagnostics.AddError("Updating Kubernetes cluster", fmt.Sprintf("Cluster %s: %s", id, err))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	if r.reload(ctx, &plan, &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *kubernetesClusterResource) update(ctx context.Context, id string, plan, state kubernetesClusterModel, diags *diag.Diagnostics) error {
	provisioning := false
	cluster, err := pollCluster(ctx, r.client, id, func(c *pidginhost.ClusterDetail) bool {
		if c != nil && c.Status == pidginhost.RESOURCESTATUSENUM_PROVISIONING {
			provisioning = true
			return false
		}
		return true
	})
	switch {
	case err != nil:
		return err
	case cluster == nil:
		return errors.New("the cluster no longer exists")
	case clusterDeleting(cluster.Status):
		return errors.New("the cluster is being deleted")
	case provisioning:
		if err := requireActive(cluster); err != nil {
			return err
		}
	}

	var body pidginhost.PatchedClusterDetailRequest
	if !plan.Name.Equal(state.Name) {
		body.Name = optionalString(plan.Name)
	}
	if !plan.Protected.Equal(state.Protected) {
		body.Protected = optionalBool(plan.Protected)
	}
	if !plan.Features.IsUnknown() && !plan.Features.Equal(state.Features) {
		body.Features = clusterFeatures(ctx, plan.Features, diags)
		if diags.HasError() {
			return nil
		}
		// The API refuses a feature change while features are still installing.
		cluster, err = pollCluster(ctx, r.client, id, func(c *pidginhost.ClusterDetail) bool {
			return c == nil || c.FeaturesReady || c.Status == pidginhost.RESOURCESTATUSENUM_FAILED
		})
		if err != nil {
			return err
		}
		if cluster == nil || !cluster.FeaturesReady {
			return errors.New("the cluster features did not become ready")
		}
	}
	if body.Name != nil || body.Protected != nil || body.Features != nil {
		if _, _, err := r.client.KubernetesAPI.KubernetesClustersPartialUpdate(ctx, id).PatchedClusterDetailRequest(body).Execute(); err != nil {
			return errors.New(describeError(err))
		}
	}
	if v := optionalString(plan.KubeVersion); v != nil && !plan.KubeVersion.Equal(state.KubeVersion) {
		if err := upgradeKubeVersion(ctx, r.client, id, *v); err != nil {
			return err
		}
	}
	if v := optionalString(plan.Encryption); v != nil && !plan.Encryption.Equal(state.Encryption) {
		return setEncryption(ctx, r.client, id, encryptionMode(*v), plan.AcknowledgeWorkloadRestart.ValueBool())
	}
	return nil
}

func (r *kubernetesClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, kubernetesClusterTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	unprotect := state.Protected.Equal(types.BoolValue(false))
	if err := deleteCluster(ctx, r.client, state.ID.ValueString(), unprotect); err != nil {
		resp.Diagnostics.AddError("Deleting Kubernetes cluster", fmt.Sprintf("Cluster %s: %s", state.ID.ValueString(), err))
	}
}

func (r *kubernetesClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// load reads the cluster and its encryption state into m. It reports false
// when the cluster is gone.
func (r *kubernetesClusterResource) load(ctx context.Context, m *kubernetesClusterModel, diags *diag.Diagnostics) bool {
	id := m.ID.ValueString()
	cluster, httpResp, err := r.client.KubernetesAPI.KubernetesClustersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		return false
	}
	if err != nil {
		diags.Append(apiError("Reading Kubernetes cluster", err))
		return false
	}
	if clusterGone(cluster) {
		return false
	}
	encryption, httpResp, err := r.client.KubernetesAPI.KubernetesClustersEncryptionRetrieve(ctx, id).Execute()
	if err != nil {
		if !isNotFound(httpResp, err) {
			diags.Append(apiError("Reading Kubernetes cluster encryption", err))
			return false
		}
		encryption = nil
	}
	m.fill(cluster, encryption)
	return true
}

// reload refreshes m after a change, when the cluster must still exist.
func (r *kubernetesClusterResource) reload(ctx context.Context, m *kubernetesClusterModel, diags *diag.Diagnostics) bool {
	found := r.load(ctx, m, diags)
	if !found && !diags.HasError() {
		diags.AddError("Reading Kubernetes cluster", fmt.Sprintf("Cluster %s disappeared.", m.ID.ValueString()))
	}
	return found && !diags.HasError()
}

func (m *kubernetesClusterModel) fill(c *pidginhost.ClusterDetail, encryption *pidginhost.ClusterEncryption) {
	m.ID = types.StringValue(strconv.Itoa(int(c.Id)))
	m.Name = types.StringValue(c.GetName())
	m.KubeVersion = types.StringPointerValue(c.KubeVersion.Get())
	features := make([]attr.Value, len(c.Features))
	for i, f := range c.Features {
		features[i] = types.StringValue(string(f))
	}
	m.Features = types.SetValueMust(types.StringType, features)
	m.Protected = types.BoolValue(c.GetProtected())
	m.Status = types.StringValue(string(c.Status))
	m.TalosVersion = types.StringPointerValue(c.TalosVersion.Get())
	m.KubeconfigValidUntil = types.StringPointerValue(c.KubeconfigValidUntil.Get())
	m.IPv4Address = types.StringPointerValue(c.Ipv4Address.Get())
	m.IPv6Address = types.StringPointerValue(c.Ipv6Address.Get())
	// These cannot change after creation, and the API ignores dual_stack where
	// the platform lacks it: keep the configured values, fill in the rest.
	if m.ClusterType.IsNull() || m.ClusterType.IsUnknown() {
		m.ClusterType = types.StringPointerValue(c.ClusterType.Get())
	}
	if m.Generation.IsNull() || m.Generation.IsUnknown() {
		m.Generation = types.StringValue(c.Generation)
	}
	if m.DualStack.IsNull() || m.DualStack.IsUnknown() {
		m.DualStack = types.BoolValue(c.DualStack)
	}
	m.Encryption = types.StringNull()
	if encryption != nil {
		m.Encryption = types.StringValue(encryptionSetting(encryption.Mode))
	}
}

func kubernetesFeatures() []string {
	names := make([]string, len(pidginhost.AllowedFeaturesEnumEnumValues))
	for i, f := range pidginhost.AllowedFeaturesEnumEnumValues {
		names[i] = string(f)
	}
	return names
}

// clusterFeatures converts a features set for the API. The slice is never nil,
// so an empty set clears the features.
func clusterFeatures(ctx context.Context, set types.Set, diags *diag.Diagnostics) []pidginhost.FeaturesEnum {
	var names []string
	diags.Append(set.ElementsAs(ctx, &names, false)...)
	features := make([]pidginhost.FeaturesEnum, len(names))
	for i, name := range names {
		features[i] = pidginhost.FeaturesEnum(name)
	}
	return features
}

// The first resource pool is described on the cluster only to create it.
// Changing those values later would otherwise replace the whole cluster, so
// the plan stops instead. Pool management is not available in this provider.
const firstPoolChanged = "The first resource pool is only described here to create the cluster, and changing it " +
	"cannot update the pool. This provider cannot manage pools yet. Resize the pool in the control panel, " +
	"and set this creation value back to %s."

type firstPoolString struct{}

func (firstPoolString) Description(context.Context) string {
	return "only used when the cluster is created"
}
func (firstPoolString) MarkdownDescription(context.Context) string {
	return "only used when the cluster is created"
}
func (firstPoolString) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.Equal(req.StateValue) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Only used when the cluster is created",
		fmt.Sprintf(firstPoolChanged, strconv.Quote(req.StateValue.ValueString())))
}

type firstPoolInt64 struct{}

func (firstPoolInt64) Description(context.Context) string {
	return "only used when the cluster is created"
}
func (firstPoolInt64) MarkdownDescription(context.Context) string {
	return "only used when the cluster is created"
}
func (firstPoolInt64) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.Equal(req.StateValue) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Only used when the cluster is created",
		fmt.Sprintf(firstPoolChanged, strconv.FormatInt(req.StateValue.ValueInt64(), 10)))
}

// pollCluster polls the cluster until done accepts it. done gets nil while the
// API answers 404.
func pollCluster(ctx context.Context, client *pidginhost.APIClient, id string, done func(*pidginhost.ClusterDetail) bool) (*pidginhost.ClusterDetail, error) {
	var cluster *pidginhost.ClusterDetail
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		c, httpResp, err := client.KubernetesAPI.KubernetesClustersRetrieve(ctx, id).Execute()
		if isNotFound(httpResp, err) {
			c, err = nil, nil
		}
		if err != nil {
			return false, errors.New(describeError(err))
		}
		cluster = c
		return done(c), nil
	})
	return cluster, err
}

func requireActive(c *pidginhost.ClusterDetail) error {
	if c == nil {
		return errors.New("the cluster no longer exists")
	}
	if c.Status != pidginhost.RESOURCESTATUSENUM_ACTIVE {
		return fmt.Errorf("the cluster is %s", c.Status)
	}
	return nil
}

// The API hides cancelled and terminated clusters but keeps listing deleting ones.
func clusterDeleting(s pidginhost.ResourceStatusEnum) bool {
	return s == pidginhost.RESOURCESTATUSENUM_CANCELLING || s == pidginhost.RESOURCESTATUSENUM_TERMINATING
}

func clusterGone(c *pidginhost.ClusterDetail) bool {
	return c == nil || c.Status == pidginhost.RESOURCESTATUSENUM_CANCELLED || c.Status == pidginhost.RESOURCESTATUSENUM_TERMINATED
}

// waitClusterSettled waits until no upgrade or feature task runs and requires
// the cluster to be active.
func waitClusterSettled(ctx context.Context, client *pidginhost.APIClient, id string) (*pidginhost.ClusterDetail, error) {
	c, err := pollCluster(ctx, client, id, func(c *pidginhost.ClusterDetail) bool {
		return c == nil || c.Status == pidginhost.RESOURCESTATUSENUM_FAILED ||
			c.Status == pidginhost.RESOURCESTATUSENUM_ACTIVE && c.FeaturesReady
	})
	if err != nil {
		return nil, err
	}
	return c, requireActive(c)
}

// upgradeKubeVersion upgrades the cluster one Kubernetes version per request
// until it runs target.
func upgradeKubeVersion(ctx context.Context, client *pidginhost.APIClient, id, target string) error {
	previous := ""
	for {
		c, err := waitClusterSettled(ctx, client, id)
		if err != nil {
			return err
		}
		current := c.GetKubeVersion()
		if previous != "" && current == previous {
			return fmt.Errorf("the upgrade from Kubernetes %s did not change the version", current)
		}
		switch order := compareVersions(current, target); {
		case order == 0:
			return nil
		case order > 0:
			return fmt.Errorf("the cluster runs Kubernetes %s, past the requested %s; downgrades are not possible", current, target)
		}
		if _, _, err := client.KubernetesAPI.KubernetesClustersKubeVersionUpgradeCreate(ctx, id).Execute(); err != nil {
			return fmt.Errorf("upgrading from Kubernetes %s: %s", current, describeError(err))
		}
		previous = current
	}
}

// compareVersions compares dotted numeric versions such as 1.35.7.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		if order := cmp.Compare(versionPart(as, i), versionPart(bs, i)); order != 0 {
			return order
		}
	}
	return 0
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(parts[i])
	return n
}

// deleteCluster deletes the cluster and waits until it is gone. A protected
// cluster is unprotected first only when unprotect is set.
func deleteCluster(ctx context.Context, client *pidginhost.APIClient, id string, unprotect bool) error {
	c, httpResp, err := client.KubernetesAPI.KubernetesClustersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) {
		return nil
	}
	if err != nil {
		return errors.New(describeError(err))
	}
	if !clusterGone(c) && !clusterDeleting(c.Status) {
		if c.GetProtected() {
			if !unprotect {
				return errors.New("the cluster is protected from deletion. Set protected = false and apply, then destroy it")
			}
			body := pidginhost.PatchedClusterDetailRequest{Protected: pidginhost.PtrBool(false)}
			if _, _, err := client.KubernetesAPI.KubernetesClustersPartialUpdate(ctx, id).PatchedClusterDetailRequest(body).Execute(); err != nil {
				return errors.New(describeError(err))
			}
		}
		httpResp, err := client.KubernetesAPI.KubernetesClustersDestroy(ctx, id).Execute()
		if err != nil && !isNotFound(httpResp, err) {
			return errors.New(describeError(err))
		}
	}
	c, err = pollCluster(ctx, client, id, func(c *pidginhost.ClusterDetail) bool {
		return c == nil || !clusterDeleting(c.Status)
	})
	if err != nil {
		return err
	}
	if !clusterGone(c) {
		return fmt.Errorf("the cluster was not deleted; it is %s", c.Status)
	}
	return nil
}

// encryptionSetting names an API encryption mode the way the encryption
// attribute does.
func encryptionSetting(mode string) string {
	switch mode {
	case "none":
		return "disabled"
	case "wireguard":
		return "enabled"
	}
	return mode
}

func encryptionMode(setting string) string {
	if setting == "enabled" {
		return "wireguard"
	}
	return "none"
}

func encryptionRunning(e *pidginhost.ClusterEncryption) bool {
	op := e.Operation.Get()
	return op != nil && (op.Status == "pending" || op.Status == "running")
}

func encryptionAt(e *pidginhost.ClusterEncryption, mode string) bool {
	settled := []pidginhost.ClusterEncryptionStatusEnum{
		pidginhost.CLUSTERENCRYPTIONSTATUSENUM_DISABLED,
		pidginhost.CLUSTERENCRYPTIONSTATUSENUM_ENABLED,
		pidginhost.CLUSTERENCRYPTIONSTATUSENUM_ENABLED_RESTART_REQUIRED,
	}
	return e.Mode == mode && slices.Contains(settled, e.Status)
}

// waitEncryption waits until no encryption change runs.
func waitEncryption(ctx context.Context, client *pidginhost.APIClient, id string) (*pidginhost.ClusterEncryption, error) {
	var state *pidginhost.ClusterEncryption
	err := waitFor(ctx, pollInterval, func() (bool, error) {
		e, _, err := client.KubernetesAPI.KubernetesClustersEncryptionRetrieve(ctx, id).Execute()
		if err != nil {
			return false, errors.New(describeError(err))
		}
		state = e
		return !encryptionRunning(e), nil
	})
	return state, err
}

// setEncryption brings the cluster to mode ("none" or "wireguard"). A change
// that is already running is awaited first.
func setEncryption(ctx context.Context, client *pidginhost.APIClient, id, mode string, ack bool) error {
	state, err := waitEncryption(ctx, client, id)
	if err != nil {
		return err
	}
	if encryptionAt(state, mode) {
		return nil
	}
	body := pidginhost.NewClusterEncryptionRequest(pidginhost.EncryptionModeEnum(mode))
	body.AcknowledgeWorkloadRestart = &ack
	if _, _, err := client.KubernetesAPI.KubernetesClustersEncryptionCreate(ctx, id).ClusterEncryptionRequest(*body).Execute(); err != nil {
		return encryptionRefusal(err)
	}
	if state, err = waitEncryption(ctx, client, id); err != nil {
		return err
	}
	reason, detail := state.Reason, state.Error
	op := state.Operation.Get()
	if op != nil {
		reason, detail = cmp.Or(op.Reason, reason), cmp.Or(op.Message, detail)
	}
	if (op == nil || op.Status != "failed") && encryptionAt(state, mode) {
		return nil
	}
	return fmt.Errorf("the encryption change failed (%s): %s", cmp.Or(reason, string(state.Status)), cmp.Or(detail, "no details"))
}

// encryptionRefusal reports the reason the API gives for refusing a change.
func encryptionRefusal(err error) error {
	var apiErr *pidginhost.GenericOpenAPIError
	if errors.As(err, &apiErr) {
		if refusal, ok := apiErr.Model().(pidginhost.ClusterEncryptionError); ok {
			reason := refusal.GetCode()
			if refusal.Reason != nil {
				reason = string(*refusal.Reason)
			}
			return fmt.Errorf("the API refused the encryption change (%s): %s", reason, refusal.Message)
		}
	}
	return errors.New(describeError(err))
}
