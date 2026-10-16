package provider

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure      = &kubernetesRouteResource{}
	_ resource.ResourceWithImportState    = &kubernetesRouteResource{}
	_ resource.ResourceWithValidateConfig = &kubernetesRouteResource{}
)

func NewKubernetesRouteResource() resource.Resource { return &kubernetesRouteResource{} }

type kubernetesRouteResource struct{ client *pidginhost.APIClient }

type kubernetesRouteModel struct {
	ID                 types.String `tfsdk:"id"`
	ClusterID          types.String `tfsdk:"cluster_id"`
	Protocol           types.String `tfsdk:"protocol"`
	Name               types.String `tfsdk:"name"`
	Namespace          types.String `tfsdk:"namespace"`
	Hostnames          types.Set    `tfsdk:"hostnames"`
	PathPrefix         types.String `tfsdk:"path_prefix"`
	EnableTLS          types.Bool   `tfsdk:"enable_tls"`
	Port               types.Int64  `tfsdk:"port"`
	BackendServiceName types.String `tfsdk:"backend_service_name"`
	BackendServicePort types.Int64  `tfsdk:"backend_service_port"`
	BackendNamespace   types.String `tfsdk:"backend_namespace"`
	StatusReady        types.Bool   `tfsdk:"status_ready"`
	StatusMessage      types.String `tfsdk:"status_message"`
}

// routeFields are the fields the HTTP, TCP and UDP routes share.
type routeFields interface {
	GetId() int32
	GetName() string
	GetNamespace() string
	GetBackendServiceName() string
	GetBackendServicePort() int32
	GetBackendNamespace() string
	GetStatusReadyOk() (*bool, bool)
	GetStatusMessage() string
}

func (r *kubernetesRouteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_route"
}

func (r *kubernetesRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	port := []validator.Int64{int64validator.Between(1, 65535)}
	resp.Schema = schema.Schema{
		Description: "A Gateway API route that exposes a Kubernetes Service through the cluster gateway. The route " +
			"the platform keeps for the Kubernetes API cannot be managed.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute(),
			"cluster_id": clusterIDAttribute(),
			"protocol": schema.StringAttribute{
				Required: true,
				Description: "Route type: http routes match host names, tcp and udp routes expose an external port. " +
					"Changing it replaces the route.",
				Validators:    []validator.String{stringvalidator.OneOf("http", "tcp", "udp")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Route name. Changing it replaces the route.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("default"),
				Description:   "Namespace of the route. Defaults to default. Changing it replaces the route.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"hostnames": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Host names the route matches. Required for http routes, not valid for others.",
			},
			"path_prefix": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Path prefix the route matches. Only for http routes; the API uses / when not set.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enable_tls": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Terminate TLS with an automatically issued certificate. Only for http routes; the API " +
					"enables it when not set.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"port": schema.Int64Attribute{
				Optional: true,
				Description: "External port to expose. Required for tcp and udp routes, not valid for http. The API " +
					"refuses the TCP ports 22, 6443, 50000 and 50001.",
				Validators: port,
			},
			"backend_service_name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the backend Service.",
			},
			"backend_service_port": schema.Int64Attribute{
				Required:    true,
				Description: "Port of the backend Service.",
				Validators:  port,
			},
			"backend_namespace": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Namespace of the backend Service. The API uses default when not set.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status_ready": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the gateway reports the route ready. Unknown while it has not reported yet.",
			},
			"status_message": schema.StringAttribute{
				Computed:    true,
				Description: "Status message from the gateway.",
			},
		},
	}
}

func (r *kubernetesRouteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *kubernetesRouteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config kubernetesRouteModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, problem := range routeConfigProblems(config) {
		resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid route setting", problem)
	}
}

// routeConfigProblems maps the attributes set for the wrong protocol, and the
// ones the protocol needs but the configuration leaves out, to the problem.
func routeConfigProblems(m kubernetesRouteModel) map[string]string {
	if m.Protocol.IsNull() || m.Protocol.IsUnknown() {
		return nil
	}
	protocol := m.Protocol.ValueString()
	problems := map[string]string{}
	if protocol == "http" {
		if !m.Port.IsNull() {
			problems["port"] = "port is only valid for tcp and udp routes."
		}
		if m.Hostnames.IsNull() {
			problems["hostnames"] = "http routes require hostnames."
		}
		return problems
	}
	for name, value := range map[string]attr.Value{"hostnames": m.Hostnames, "path_prefix": m.PathPrefix, "enable_tls": m.EnableTLS} {
		if !value.IsNull() {
			problems[name] = name + " is only valid for http routes."
		}
	}
	if m.Port.IsNull() {
		problems["port"] = protocol + " routes require port."
	}
	return problems
}

// platformRoute reports the route the platform keeps for the Kubernetes API.
func platformRoute(protocol, name, namespace string) bool {
	return protocol == "tcp" && name == "kube-apiserver" && namespace == "envoy-gw"
}

func (r *kubernetesRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubernetesRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := int32ID(plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	hostnames := routeHostnames(ctx, plan.Hostnames, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	api := r.client.KubernetesAPI
	name, namespace := plan.Name.ValueString(), optionalString(plan.Namespace)
	backendName, backendPort := plan.BackendServiceName.ValueString(), int32(plan.BackendServicePort.ValueInt64())
	backendNamespace, port := optionalString(plan.BackendNamespace), int32(plan.Port.ValueInt64())
	var route routeFields
	switch plan.Protocol.ValueString() {
	case "http":
		body := pidginhost.HTTPRouteRequest{
			Name: name, Namespace: namespace, Hostnames: hostnames,
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
			PathPrefix: optionalString(plan.PathPrefix), EnableTls: optionalBool(plan.EnableTLS),
		}
		route, _, err = api.KubernetesClustersHttproutesCreate(ctx, clusterID).HTTPRouteRequest(body).Execute()
	case "tcp":
		body := pidginhost.TCPRouteRequest{
			Name: name, Namespace: namespace, Port: port,
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
		}
		route, _, err = api.KubernetesClustersTcproutesCreate(ctx, clusterID).TCPRouteRequest(body).Execute()
	default:
		body := pidginhost.UDPRouteRequest{
			Name: name, Namespace: namespace, Port: port,
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
		}
		route, _, err = api.KubernetesClustersUdproutesCreate(ctx, clusterID).UDPRouteRequest(body).Execute()
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating Kubernetes route", err))
		return
	}
	plan.fill(route)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubernetesRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := int32ID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	api, id := r.client.KubernetesAPI, state.ID.ValueString()
	var route routeFields
	var httpResp *http.Response
	switch state.Protocol.ValueString() {
	case "http":
		route, httpResp, err = api.KubernetesClustersHttproutesRetrieve(ctx, clusterID, id).Execute()
	case "tcp":
		route, httpResp, err = api.KubernetesClustersTcproutesRetrieve(ctx, clusterID, id).Execute()
	default:
		route, httpResp, err = api.KubernetesClustersUdproutesRetrieve(ctx, clusterID, id).Execute()
	}
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading Kubernetes route", err))
		return
	}
	if platformRoute(state.Protocol.ValueString(), route.GetName(), route.GetNamespace()) {
		resp.Diagnostics.AddError("Platform route", fmt.Sprintf(
			"Route %s is the route the platform keeps for the Kubernetes API. Terraform does not manage it.", id))
		return
	}
	state.fill(route)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *kubernetesRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kubernetesRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	clusterID, err := int32ID(plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	hostnames := routeHostnames(ctx, plan.Hostnames, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	api, id := r.client.KubernetesAPI, plan.ID.ValueString()
	backendName := plan.BackendServiceName.ValueStringPointer()
	backendPort := optionalInt32(plan.BackendServicePort)
	backendNamespace, port := optionalString(plan.BackendNamespace), optionalInt32(plan.Port)
	var route routeFields
	switch plan.Protocol.ValueString() {
	case "http":
		body := pidginhost.PatchedHTTPRouteRequest{
			Hostnames:          hostnames,
			PathPrefix:         optionalString(plan.PathPrefix),
			EnableTls:          optionalBool(plan.EnableTLS),
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
		}
		route, _, err = api.KubernetesClustersHttproutesPartialUpdate(ctx, clusterID, id).PatchedHTTPRouteRequest(body).Execute()
	case "tcp":
		body := pidginhost.PatchedTCPRouteRequest{
			Port:               port,
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
		}
		route, _, err = api.KubernetesClustersTcproutesPartialUpdate(ctx, clusterID, id).PatchedTCPRouteRequest(body).Execute()
	default:
		body := pidginhost.PatchedUDPRouteRequest{
			Port:               port,
			BackendServiceName: backendName, BackendServicePort: backendPort, BackendNamespace: backendNamespace,
		}
		route, _, err = api.KubernetesClustersUdproutesPartialUpdate(ctx, clusterID, id).PatchedUDPRouteRequest(body).Execute()
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Updating Kubernetes route", err))
		return
	}
	plan.fill(route)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *kubernetesRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kubernetesRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	protocol := state.Protocol.ValueString()
	if platformRoute(protocol, state.Name.ValueString(), state.Namespace.ValueString()) {
		resp.Diagnostics.AddError("Platform route", "The route the platform keeps for the Kubernetes API is never deleted.")
		return
	}
	clusterID, err := int32ID(state.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("cluster_id"), "Invalid cluster ID", err.Error())
		return
	}
	api, id := r.client.KubernetesAPI, state.ID.ValueString()
	var httpResp *http.Response
	switch protocol {
	case "http":
		httpResp, err = api.KubernetesClustersHttproutesDestroy(ctx, clusterID, id).Execute()
	case "tcp":
		httpResp, err = api.KubernetesClustersTcproutesDestroy(ctx, clusterID, id).Execute()
	default:
		httpResp, err = api.KubernetesClustersUdproutesDestroy(ctx, clusterID, id).Execute()
	}
	if err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Deleting Kubernetes route", err))
	}
}

func (r *kubernetesRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 3)
	if !ok || !slices.Contains([]string{"http", "tcp", "udp"}, parts[1]) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf(
			"Expected <cluster_id>/<protocol>/<route_id> with protocol http, tcp or udp, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("protocol"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[2])...)
}

// routeHostnames converts the hostnames set for the API; nil when not set.
func routeHostnames(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var hostnames []string
	diags.Append(set.ElementsAs(ctx, &hostnames, false)...)
	return hostnames
}

func (m *kubernetesRouteModel) fill(route routeFields) {
	m.ID = types.StringValue(strconv.Itoa(int(route.GetId())))
	m.Name = types.StringValue(route.GetName())
	// The API leaves both namespaces out when they are the default one.
	m.Namespace = types.StringValue(cmp.Or(route.GetNamespace(), "default"))
	m.BackendNamespace = types.StringValue(cmp.Or(route.GetBackendNamespace(), "default"))
	m.BackendServiceName = types.StringValue(route.GetBackendServiceName())
	m.BackendServicePort = types.Int64Value(int64(route.GetBackendServicePort()))
	ready, _ := route.GetStatusReadyOk()
	m.StatusReady = types.BoolPointerValue(ready)
	m.StatusMessage = types.StringValue(route.GetStatusMessage())
	m.Hostnames = types.SetNull(types.StringType)
	m.PathPrefix = types.StringNull()
	m.EnableTLS = types.BoolNull()
	m.Port = types.Int64Null()
	switch r := route.(type) {
	case *pidginhost.HTTPRoute:
		hostnames := make([]attr.Value, len(r.Hostnames))
		for i, h := range r.Hostnames {
			hostnames[i] = types.StringValue(h)
		}
		m.Hostnames = types.SetValueMust(types.StringType, hostnames)
		m.PathPrefix = types.StringPointerValue(r.PathPrefix)
		m.EnableTLS = types.BoolPointerValue(r.EnableTls)
	case interface{ GetPort() int32 }:
		m.Port = types.Int64Value(int64(r.GetPort()))
	}
}
