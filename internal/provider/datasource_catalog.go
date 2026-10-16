package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &catalogDataSource{}

// NewCatalogDataSource keeps the JSON string data source of provider 0.1.x
// working. New attributes belong in the typed data sources.
func NewCatalogDataSource() datasource.DataSource { return &catalogDataSource{} }

type catalogDataSource struct{ dataSourceBase }

type catalogModel struct {
	ImagesJSON           string `tfsdk:"images_json"`
	PackagesJSON         string `tfsdk:"packages_json"`
	ProfileJSON          string `tfsdk:"profile_json"`
	VolumeProductsJSON   string `tfsdk:"volume_products_json"`
	VolumesJSON          string `tfsdk:"volumes_json"`
	SSHKeysJSON          string `tfsdk:"ssh_keys_json"`
	FirewallsJSON        string `tfsdk:"firewalls_json"`
	IPsJSON              string `tfsdk:"ips_json"`
	PublicInterfacesJSON string `tfsdk:"public_interfaces_json"`
	ServersJSON          string `tfsdk:"servers_json"`
}

func (d *catalogDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_catalog"
}

func (d *catalogDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Account and catalog listings as JSON strings, for configurations written for provider 0.1.x. " +
			"Decode them with jsondecode.",
		DeprecationMessage: "pidginhost_catalog is kept only for configurations written for provider 0.1.x. Use the " +
			"typed data sources instead: pidginhost_images, pidginhost_packages, pidginhost_generations, " +
			"pidginhost_storage_products, pidginhost_kubernetes_cluster_types, pidginhost_ssh_key and pidginhost_server.",
		Attributes: map[string]schema.Attribute{
			"images_json":   computedString("JSON array of the available server images."),
			"packages_json": computedString("JSON array of the available server packages."),
			"profile_json": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				Description: "JSON object with the account profile (name, phone number and funds). Sensitive, since " +
					"it holds personal data.",
			},
			"volume_products_json": computedString("JSON array of the available storage products (volume tiers)."),
			"volumes_json":         computedString("JSON array of the volumes in the account."),
			"ssh_keys_json":        computedString("JSON array of the SSH keys stored on the account."),
			"firewalls_json":       computedString("JSON array of the firewall rule sets in the account."),
			"ips_json":             computedString("JSON array of the public IPv4 addresses in the account."),
			"public_interfaces_json": computedString("JSON array with the public interface and firewall settings of " +
				"every server that has a public address, each entry tagged with the server_id and hostname of its server."),
			"servers_json": computedString("JSON array of the servers in the account."),
		},
	}
}

func (d *catalogDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model catalogModel
	var servers []pidginhost.Server
	cloud := d.client.CloudAPI
	lists := []struct {
		what   string
		target *string
		fetch  func() (any, error)
	}{
		{"images", &model.ImagesJSON, func() (any, error) { return listImages(ctx, d.client) }},
		{"server packages", &model.PackagesJSON, func() (any, error) { return listPackages(ctx, d.client, "") }},
		{"account profile", &model.ProfileJSON, func() (any, error) {
			profile, _, err := d.client.AccountAPI.AccountProfileRetrieve(ctx).Execute()
			return profile, err
		}},
		{"storage products", &model.VolumeProductsJSON, func() (any, error) { return listStorageProducts(ctx, d.client) }},
		{"volumes", &model.VolumesJSON, func() (any, error) {
			volumes, _, err := cloud.CloudVolumesList(ctx).Execute()
			return volumes, err
		}},
		{"SSH keys", &model.SSHKeysJSON, func() (any, error) { return listSSHKeys(ctx, d.client) }},
		{"firewall rule sets", &model.FirewallsJSON, func() (any, error) {
			sets, _, err := cloud.CloudFirewallRulesSetList(ctx).Execute()
			return sets, err
		}},
		{"public IPv4 addresses", &model.IPsJSON, func() (any, error) {
			return listAll(func(page int32) (*pidginhost.PaginatedPublicIPv4List, *http.Response, error) {
				return cloud.CloudIpv4List(ctx).Page(page).Execute()
			})
		}},
		{"servers", &model.ServersJSON, func() (any, error) {
			var err error
			servers, err = listServers(ctx, d.client)
			return servers, err
		}},
		{"public interfaces", &model.PublicInterfacesJSON, func() (any, error) {
			return listPublicInterfaces(ctx, d.client, servers)
		}},
	}
	for _, list := range lists {
		value, err := list.fetch()
		if err != nil {
			resp.Diagnostics.Append(apiError("Reading "+list.what, err))
			return
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			resp.Diagnostics.AddError("Encoding "+list.what, err.Error())
			return
		}
		*list.target = string(encoded)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

// listPublicInterfaces reads the public interface of every live server with a
// public address. The API has no account wide list (provider 0.1.x asked for
// one, got 404 and always returned []), so entries carry their server.
func listPublicInterfaces(ctx context.Context, client *pidginhost.APIClient, servers []pidginhost.Server) ([]map[string]any, error) {
	interfaces := []map[string]any{}
	for _, server := range servers {
		public := server.Networks.Public
		if serverIsGone(server.GetStatus()) || public.GetIpv4() == "" && public.GetIpv6() == "" {
			continue
		}
		iface, httpResp, err := client.CloudAPI.CloudServersPublicInterfaceRetrieve(ctx, server.Id).Execute()
		if isNotFound(httpResp, err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entry, err := iface.ToMap()
		if err != nil {
			return nil, err
		}
		entry["server_id"] = server.Id
		entry["hostname"] = server.GetHostname()
		interfaces = append(interfaces, entry)
	}
	return interfaces, nil
}
