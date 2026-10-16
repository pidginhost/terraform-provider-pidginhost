package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &publicIPResource{}
	_ resource.ResourceWithImportState = &publicIPResource{}
)

func NewPublicIPResource() resource.Resource { return &publicIPResource{} }

type publicIPResource struct{ client *pidginhost.APIClient }

type publicIPModel struct {
	ID             types.String   `tfsdk:"id"`
	Version        types.Int64    `tfsdk:"version"`
	ServerID       types.String   `tfsdk:"server_id"`
	ReverseDNS     types.String   `tfsdk:"reverse_dns"`
	RebootOnAttach types.Bool     `tfsdk:"reboot_on_attach"`
	Address        types.String   `tfsdk:"address"`
	Gateway        types.String   `tfsdk:"gateway"`
	Prefix         types.Int64    `tfsdk:"prefix"`
	Timeouts       timeouts.Value `tfsdk:"timeouts"`
}

// ipServerKey is the private state key holding the server this resource
// attached the address to, "" for none.
const ipServerKey = "attached_server_id"

func (r *publicIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_public_ip"
}

func (r *publicIPResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A public IPv4 or IPv6 address allocated to the account, optionally attached to a cloud server. " +
			"Every new resource allocates a new address.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"version": schema.Int64Attribute{
				Required:      true,
				Description:   "IP version: 4 or 6. Changing it replaces the address.",
				Validators:    []validator.Int64{int64validator.OneOf(4, 6)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"server_id": schema.StringAttribute{
				Optional: true,
				Description: "ID of the server to attach the address to. Unset keeps the address unattached. Changing " +
					"it moves the address in place. Terraform refuses to take the address off a server it did not " +
					"attach it to.",
				Validators: []validator.String{numericID},
			},
			"reverse_dns": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Reverse DNS (PTR) name of the address. Attaching the address to a server sets it to the " +
					"server hostname unless it is set here.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)},
			},
			"reboot_on_attach": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Reboot the server when attaching the address, so its operating system picks the address " +
					"up. Defaults to false.",
			},
			"address": schema.StringAttribute{
				Computed:      true,
				Description:   "The IP address.",
				PlanModifiers: computed,
			},
			"gateway": schema.StringAttribute{
				Computed:      true,
				Description:   "Gateway of the address.",
				PlanModifiers: computed,
			},
			"prefix": schema.Int64Attribute{
				Computed:      true,
				Description:   "Prefix length of the address network.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Update: true}),
		},
	}
}

func (r *publicIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *publicIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan publicIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	version := plan.Version.ValueInt64()
	ip, err := r.allocate(ctx, version)
	if err != nil {
		resp.Diagnostics.Append(apiError("Allocating public IP", err))
		return
	}
	// Every allocation is a new billed address: keep it in state before
	// anything else can fail.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.Itoa(int(ip.Id)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), version)...)

	server := plan.ServerID.ValueString()
	if server != "" {
		rebootNeeded, err := r.bind(ctx, version, ip, "", server, plan.RebootOnAttach.ValueBool())
		if err != nil {
			resp.Diagnostics.Append(apiError("Attaching public IP", err))
			return
		}
		warnReboot(&resp.Diagnostics, rebootNeeded)
	}
	resp.Diagnostics.Append(setManagedServer(ctx, resp.Private, server)...)
	if !plan.ReverseDNS.IsUnknown() && !plan.ReverseDNS.IsNull() {
		if err := r.setReverseDNS(ctx, version, ip.Id, plan.ReverseDNS.ValueString()); err != nil {
			resp.Diagnostics.Append(apiError("Setting reverse DNS", err))
			return
		}
	}
	r.readInto(ctx, &plan, ip.Id, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *publicIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state publicIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseInt32ID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid public IP id", err.Error())
		return
	}
	ip, httpResp, err := r.get(ctx, state.Version.ValueInt64(), id)
	if isNotFound(httpResp, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading public IP", err))
		return
	}
	if err := r.fill(ctx, &state, ip); err != nil {
		resp.Diagnostics.Append(apiError("Reading reverse DNS", err))
		return
	}
	// An imported address was attached by someone else; adopt the binding it
	// has now, so Terraform can move it later.
	if _, known := managedServer(ctx, req.Private); !known {
		resp.Diagnostics.Append(setManagedServer(ctx, resp.Private, boundServer(ip))...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *publicIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state publicIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseInt32ID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid public IP id", err.Error())
		return
	}
	version := state.Version.ValueInt64()

	if !plan.ServerID.Equal(state.ServerID) {
		timeout, diags := plan.Timeouts.Update(ctx, computeDefaultTimeout)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ip, _, err := r.get(waitCtx, version, id)
		if err != nil {
			resp.Diagnostics.Append(apiError("Reading public IP", err))
			return
		}
		managed, _ := managedServer(ctx, req.Private)
		server := plan.ServerID.ValueString()
		rebootNeeded, err := r.bind(waitCtx, version, ip, managed, server, plan.RebootOnAttach.ValueBool())
		if err != nil {
			resp.Diagnostics.Append(apiError("Moving public IP", err))
			return
		}
		warnReboot(&resp.Diagnostics, rebootNeeded)
		resp.Diagnostics.Append(setManagedServer(ctx, resp.Private, server)...)
	}

	// Attaching resets the PTR to the server hostname, so compare with the
	// API rather than with the state.
	if !plan.ReverseDNS.IsUnknown() && !plan.ReverseDNS.IsNull() {
		current, err := r.reverseDNS(ctx, version, id)
		if err == nil && current != plan.ReverseDNS.ValueString() {
			err = r.setReverseDNS(ctx, version, id, plan.ReverseDNS.ValueString())
		}
		if err != nil {
			resp.Diagnostics.Append(apiError("Setting reverse DNS", err))
			return
		}
	}
	r.readInto(ctx, &plan, id, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *publicIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state publicIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseInt32ID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid public IP id", err.Error())
		return
	}
	version := state.Version.ValueInt64()
	ip, httpResp, err := r.get(ctx, version, id)
	if isNotFound(httpResp, err) {
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading public IP", err))
		return
	}
	if ipBound(ip) {
		if httpResp, err := r.detach(ctx, version, id); err != nil && !isNotFound(httpResp, err) {
			resp.Diagnostics.Append(apiError("Detaching public IP", err))
			return
		}
	}
	if httpResp, err := r.release(ctx, version, id); err != nil && !isNotFound(httpResp, err) {
		resp.Diagnostics.Append(apiError("Releasing public IP", err))
	}
}

func (r *publicIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importIPVersionAndID(ctx, req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("reboot_on_attach"), false)...)
}

// planIPBinding decides how to move an address from where the API has it
// (bound, and boundTo naming the server id when known) to want ("" for no
// server). managed is the server this resource attached it to. It refuses to
// take the address off any other server.
func planIPBinding(bound bool, boundTo, managed, want string) (detach, attach bool, err error) {
	if bound && boundTo == want {
		return false, false, nil
	}
	if bound && boundTo != managed {
		where := "server " + boundTo
		if boundTo == "" {
			where = "a server outside the cloud servers"
		}
		return false, false, fmt.Errorf("the address is attached to %s, which Terraform did not attach it to; "+
			"detach it there first", where)
	}
	return bound, want != "", nil
}

// bind moves the address to the server want ("" detaches it). After a detach
// it waits until the API no longer binds the address. It reports whether the
// server needs a reboot to see an attached address.
func (r *publicIPResource) bind(ctx context.Context, version int64, ip *pidginhost.PublicIPv4, managed, want string, reboot bool) (bool, error) {
	detach, attach, err := planIPBinding(ipBound(ip), boundServer(ip), managed, want)
	if err != nil {
		return false, err
	}
	if detach {
		if httpResp, err := r.detach(ctx, version, ip.Id); err != nil && !isNotFound(httpResp, err) {
			return false, err
		}
		err := waitFor(ctx, computePollInterval, func() (bool, error) {
			got, _, err := r.get(ctx, version, ip.Id)
			return err == nil && !ipBound(got), err
		})
		if err != nil {
			return false, fmt.Errorf("waiting for the detach: %w", err)
		}
	}
	if !attach {
		return false, nil
	}
	serverID, err := parseInt32ID(want)
	if err != nil {
		return false, err
	}
	if version == 6 {
		body := pidginhost.AttachIPv6Request{Ipv6: ip.Address, Reboot: &reboot}
		res, _, err := r.client.CloudAPI.CloudServersAttachIpv6Create(ctx, serverID).AttachIPv6Request(body).Execute()
		if err != nil {
			return false, err
		}
		return res.RebootRequired && !res.Rebooted, nil
	}
	body := pidginhost.AttachIPv4Request{Ipv4: ip.Address, Reboot: &reboot}
	res, _, err := r.client.CloudAPI.CloudServersAttachIpv4Create(ctx, serverID).AttachIPv4Request(body).Execute()
	if err != nil {
		return false, err
	}
	return res.RebootRequired && !res.Rebooted, nil
}

// readInto reads the address after a change and fills m.
func (r *publicIPResource) readInto(ctx context.Context, m *publicIPModel, id int32, diags *diag.Diagnostics) {
	ip, _, err := r.get(ctx, m.Version.ValueInt64(), id)
	if err == nil {
		err = r.fill(ctx, m, ip)
	}
	if err != nil {
		diags.Append(apiError("Reading public IP", err))
	}
}

// fill copies ip into m, with the PTR, which the API serves separately.
func (r *publicIPResource) fill(ctx context.Context, m *publicIPModel, ip *pidginhost.PublicIPv4) error {
	rdns, err := r.reverseDNS(ctx, m.Version.ValueInt64(), ip.Id)
	if err != nil {
		return err
	}
	m.ID = types.StringValue(strconv.Itoa(int(ip.Id)))
	m.Address = types.StringValue(ip.Address)
	m.Gateway = types.StringValue(ip.Gateway)
	m.Prefix = types.Int64Value(int64(ip.Prefix))
	m.ReverseDNS = types.StringValue(rdns)
	m.ServerID = types.StringNull()
	if server := boundServer(ip); server != "" {
		m.ServerID = types.StringValue(server)
	}
	return nil
}

func warnReboot(diags *diag.Diagnostics, needed bool) {
	if needed {
		diags.AddWarning("Server needs a reboot",
			"The address is attached, but the server only sees it after a reboot. Set reboot_on_attach to reboot on attach.")
	}
}

// ipBound reports whether the address is on a server. attached flips only
// once the network change is applied; server_id names the server before that.
func ipBound(ip *pidginhost.PublicIPv4) bool {
	return ip.Attached || ip.ServerId.Get() != nil
}

func boundServer(ip *pidginhost.PublicIPv4) string {
	if id := ip.ServerId.Get(); id != nil {
		return strconv.Itoa(int(*id))
	}
	return ""
}

// privateState is the resource private state the framework hands over.
type privateState interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

// managedServer returns the server this resource attached the address to,
// and false when it never recorded one (an imported address).
func managedServer(ctx context.Context, p privateState) (string, bool) {
	raw, _ := p.GetKey(ctx, ipServerKey)
	var server string
	if raw == nil || json.Unmarshal(raw, &server) != nil {
		return "", false
	}
	return server, true
}

func setManagedServer(ctx context.Context, p privateState, server string) diag.Diagnostics {
	raw, _ := json.Marshal(server)
	return p.SetKey(ctx, ipServerKey, raw)
}

// The API has separate IPv4 and IPv6 endpoints with identical models; these
// helpers pick one and return the IPv4 model.

func (r *publicIPResource) get(ctx context.Context, version int64, id int32) (*pidginhost.PublicIPv4, *http.Response, error) {
	if version == 6 {
		ip, httpResp, err := r.client.CloudAPI.CloudIpv6Retrieve(ctx, id).Execute()
		if err != nil {
			return nil, httpResp, err
		}
		v4 := pidginhost.PublicIPv4(*ip)
		return &v4, httpResp, nil
	}
	return r.client.CloudAPI.CloudIpv4Retrieve(ctx, id).Execute()
}

func (r *publicIPResource) allocate(ctx context.Context, version int64) (*pidginhost.PublicIPv4, error) {
	if version == 6 {
		ip, _, err := r.client.CloudAPI.CloudIpv6Create(ctx).Execute()
		if err != nil {
			return nil, err
		}
		v4 := pidginhost.PublicIPv4(*ip)
		return &v4, nil
	}
	ip, _, err := r.client.CloudAPI.CloudIpv4Create(ctx).Execute()
	return ip, err
}

func (r *publicIPResource) detach(ctx context.Context, version int64, id int32) (*http.Response, error) {
	if version == 6 {
		_, httpResp, err := r.client.CloudAPI.CloudIpv6DetachCreate(ctx, id).Execute()
		return httpResp, err
	}
	_, httpResp, err := r.client.CloudAPI.CloudIpv4DetachCreate(ctx, id).Execute()
	return httpResp, err
}

func (r *publicIPResource) release(ctx context.Context, version int64, id int32) (*http.Response, error) {
	if version == 6 {
		return r.client.CloudAPI.CloudIpv6Destroy(ctx, id).Execute()
	}
	return r.client.CloudAPI.CloudIpv4Destroy(ctx, id).Execute()
}

func (r *publicIPResource) reverseDNS(ctx context.Context, version int64, id int32) (string, error) {
	var res *pidginhost.ReverseDNS
	var err error
	if version == 6 {
		res, _, err = r.client.CloudAPI.CloudIpv6RdnsRetrieve(ctx, id).Execute()
	} else {
		res, _, err = r.client.CloudAPI.CloudIpv4RdnsRetrieve(ctx, id).Execute()
	}
	if err != nil {
		return "", err
	}
	return res.ReverseDns, nil
}

func (r *publicIPResource) setReverseDNS(ctx context.Context, version int64, id int32, name string) error {
	body := *pidginhost.NewReverseDNSRequest(name)
	var err error
	if version == 6 {
		_, _, err = r.client.CloudAPI.CloudIpv6RdnsCreate(ctx, id).ReverseDNSRequest(body).Execute()
	} else {
		_, _, err = r.client.CloudAPI.CloudIpv4RdnsCreate(ctx, id).ReverseDNSRequest(body).Execute()
	}
	return err
}
