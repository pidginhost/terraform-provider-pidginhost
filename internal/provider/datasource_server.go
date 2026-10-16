package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ datasource.DataSourceWithConfigure        = &serverDataSource{}
	_ datasource.DataSourceWithConfigValidators = &serverDataSource{}
)

func NewServerDataSource() datasource.DataSource { return &serverDataSource{} }

type serverDataSource struct{ dataSourceBase }

type serverDataModel struct {
	ID                string                      `tfsdk:"id"`
	Hostname          string                      `tfsdk:"hostname"`
	Status            string                      `tfsdk:"status"`
	Project           *string                     `tfsdk:"project"`
	Image             string                      `tfsdk:"image"`
	Package           string                      `tfsdk:"package"`
	Generation        string                      `tfsdk:"generation"`
	Cpus              int32                       `tfsdk:"cpus"`
	Memory            int32                       `tfsdk:"memory"`
	DiskSize          int32                       `tfsdk:"disk_size"`
	Username          string                      `tfsdk:"username"`
	IPv4              string                      `tfsdk:"ipv4"`
	IPv6              string                      `tfsdk:"ipv6"`
	PrivateNetworks   []serverPrivateNetworkModel `tfsdk:"private_networks"`
	DestroyProtection bool                        `tfsdk:"destroy_protection"`
}

type serverPrivateNetworkModel struct {
	Network   string `tfsdk:"network"`
	Address   string `tfsdk:"address"`
	Interface string `tfsdk:"interface"`
}

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up one server by id or hostname. Set exactly one of them; the lookup fails when no server " +
			"or more than one server matches. Cancelled and terminated servers do not match.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Id of the server.",
			},
			"hostname": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Hostname of the server, compared without regard to case.",
			},
			"status":     computedString("Status of the server, such as active, provisioning or suspended."),
			"project":    computedString("Project the server belongs to. Null when it has none."),
			"image":      computedString("Image the server was installed from."),
			"package":    computedString("Package (size) of the server."),
			"generation": computedString("Hardware generation the server runs on."),
			"cpus":       computedInt("Number of vCPUs."),
			"memory":     computedInt("Memory, in GB."),
			"disk_size":  computedInt("Disk size, in GB."),
			"username":   computedString("Account set up for SSH login."),
			"ipv4":       computedString("Primary public IPv4 address. Empty when the server has none."),
			"ipv6":       computedString("Primary public IPv6 address. Empty when the server has none."),
			"private_networks": computedList("Private networks the server is a member of.", map[string]schema.Attribute{
				"network":   computedString("Address of the private network, in CIDR notation."),
				"address":   computedString("Address of the server in that network."),
				"interface": computedString("Network interface of the server in that network."),
			}),
			"destroy_protection": computedBool("Whether the server is protected against being destroyed."),
		},
	}
}

func (d *serverDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("hostname")),
	}
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var idValue, hostname types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &idValue)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("hostname"), &hostname)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var id int32
	query := fmt.Sprintf("id %q", idValue.ValueString())
	if idValue.IsNull() {
		query = fmt.Sprintf("hostname %q", hostname.ValueString())
		servers, err := listServers(ctx, d.client)
		if err != nil {
			resp.Diagnostics.Append(apiError("Listing servers", err))
			return
		}
		if id, err = findServerByHostname(servers, hostname.ValueString()); err != nil {
			resp.Diagnostics.AddError("Looking up server", err.Error())
			return
		}
	} else {
		n, err := strconv.ParseInt(idValue.ValueString(), 10, 32)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("id"), "Invalid server id", "id must be a number, got "+query+".")
			return
		}
		id = int32(n)
	}

	server, httpResp, err := d.client.CloudAPI.CloudServersRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) || err == nil && serverIsGone(server.Status) {
		resp.Diagnostics.AddError("Looking up server", "no server matches "+query)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading server", err))
		return
	}

	private := make([]serverPrivateNetworkModel, len(server.Networks.Private))
	for i, p := range server.Networks.Private {
		private[i] = serverPrivateNetworkModel{Network: p.Network, Address: p.Address, Interface: p.Interface}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, serverDataModel{
		ID:                strconv.Itoa(int(server.Id)),
		Hostname:          server.Hostname,
		Status:            string(server.Status),
		Project:           server.Project,
		Image:             server.Image,
		Package:           server.Package,
		Generation:        server.Generation,
		Cpus:              server.Cpus,
		Memory:            server.Memory,
		DiskSize:          server.DiskSize,
		Username:          server.Username,
		IPv4:              server.Networks.Public.GetIpv4(),
		IPv6:              server.Networks.Public.GetIpv6(),
		PrivateNetworks:   private,
		DestroyProtection: server.DestroyProtection,
	})...)
}

// findServerByHostname returns the id of the one server named hostname,
// ignoring case and servers that are cancelled or terminated.
func findServerByHostname(servers []pidginhost.Server, hostname string) (int32, error) {
	var matches []pidginhost.Server
	for _, s := range servers {
		if strings.EqualFold(s.GetHostname(), hostname) && !serverIsGone(s.GetStatus()) {
			matches = append(matches, s)
		}
	}
	server, err := onlyMatch(matches, func(s pidginhost.Server) int32 { return s.Id }, "server", fmt.Sprintf("hostname %q", hostname))
	return server.Id, err
}

func serverIsGone(status pidginhost.ResourceStatusEnum) bool {
	return status == pidginhost.RESOURCESTATUSENUM_CANCELLED || status == pidginhost.RESOURCESTATUSENUM_TERMINATED
}

func listServers(ctx context.Context, client *pidginhost.APIClient) ([]pidginhost.Server, error) {
	return listAll(func(page int32) (*pidginhost.PaginatedServerList, *http.Response, error) {
		return client.CloudAPI.CloudServersList(ctx).Page(page).Execute()
	})
}
