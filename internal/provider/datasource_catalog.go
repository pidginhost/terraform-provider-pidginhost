package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func NewCatalogDataSource() datasource.DataSource {
	return &catalogDataSource{}
}

type catalogDataSource struct {
	client *Client
}

type catalogDataSourceModel struct {
	ImagesJSON           types.String `tfsdk:"images_json"`
	PackagesJSON         types.String `tfsdk:"packages_json"`
	ProfileJSON          types.String `tfsdk:"profile_json"`
	VolumeProductsJSON   types.String `tfsdk:"volume_products_json"`
	VolumesJSON          types.String `tfsdk:"volumes_json"`
	SSHKeysJSON          types.String `tfsdk:"ssh_keys_json"`
	FirewallsJSON        types.String `tfsdk:"firewalls_json"`
	IPsJSON              types.String `tfsdk:"ips_json"`
	PublicInterfacesJSON types.String `tfsdk:"public_interfaces_json"`
	ServersJSON          types.String `tfsdk:"servers_json"`
}

func (d *catalogDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_catalog"
}

func (d *catalogDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches catalog information such as available images and packages from the PidginHost API.",
		Attributes: map[string]schema.Attribute{
			"images_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing the available images.",
			},
			"packages_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing the available packages.",
			},
			"profile_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON object containing account profile information.",
			},
			"volume_products_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing available volume products (storage tiers).",
			},
			"volumes_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing existing volumes in the account.",
			},
			"ssh_keys_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing uploaded SSH keys.",
			},
			"firewalls_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing firewall rule sets.",
			},
			"ips_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing public IP addresses managed by the account.",
			},
			"public_interfaces_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing public interface details.",
			},
			"servers_json": schema.StringAttribute{
				Computed:    true,
				Description: "JSON array containing existing servers in the account.",
			},
		},
	}
}

func (d *catalogDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data Type", "Expected *Client for provider data")
		return
	}

	d.client = client
}

func (d *catalogDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Provider", "The provider must be configured before using this data source.")
		return
	}

	var state catalogDataSourceModel

	images, err := d.client.API.ListImages(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch images", err.Error())
		return
	}

	packages, err := d.client.API.ListPackages(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch packages", err.Error())
		return
	}

	profile, err := d.client.API.GetProfile(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch profile", err.Error())
		return
	}

	volumeProducts, err := d.client.API.ListVolumeProducts(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch volume products", err.Error())
		return
	}

	volumes, err := d.client.API.ListVolumes(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch volumes", err.Error())
		return
	}

	sshKeys, err := d.client.API.ListSSHKeys(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch SSH keys", err.Error())
		return
	}

	firewalls, err := d.client.API.ListFirewalls(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch firewall sets", err.Error())
		return
	}

	ips, err := d.client.API.ListIPs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch IPs", err.Error())
		return
	}

	publicInterfaces, err := d.client.API.ListPublicInterfaces(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch public interfaces", err.Error())
		return
	}

	servers, err := d.client.API.ListServers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch servers", err.Error())
		return
	}

	imagesJSON, diag := marshalCatalogToJSON("images_json", images)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	packagesJSON, diag := marshalCatalogToJSON("packages_json", packages)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.ImagesJSON = types.StringValue(imagesJSON)
	state.PackagesJSON = types.StringValue(packagesJSON)

	profileJSON, diag := marshalObjectToJSON("profile_json", profile)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ProfileJSON = types.StringValue(profileJSON)

	volumeProductsJSON, diag := marshalCatalogToJSON("volume_products_json", volumeProducts)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.VolumeProductsJSON = types.StringValue(volumeProductsJSON)

	volumesJSON, diag := marshalAnyToJSON("volumes_json", volumes)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.VolumesJSON = types.StringValue(volumesJSON)

	sshKeysJSON, diag := marshalCatalogToJSON("ssh_keys_json", sshKeys)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.SSHKeysJSON = types.StringValue(sshKeysJSON)

	firewallsJSON, diag := marshalCatalogToJSON("firewalls_json", firewalls)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.FirewallsJSON = types.StringValue(firewallsJSON)

	ipsJSON, diag := marshalCatalogToJSON("ips_json", ips)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.IPsJSON = types.StringValue(ipsJSON)

	publicInterfacesJSON, diag := marshalAnySliceToJSON("public_interfaces_json", publicInterfaces)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.PublicInterfacesJSON = types.StringValue(publicInterfacesJSON)

	serversJSON, diag := marshalCatalogToJSON("servers_json", servers)
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ServersJSON = types.StringValue(serversJSON)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func marshalCatalogToJSON(attributeName string, data []map[string]any) (string, diag.Diagnostics) {
	diagnostics := diag.Diagnostics{}
	payload, err := json.Marshal(data)
	if err != nil {
		diagnostics.AddAttributeError(
			path.Root(attributeName),
			"Failed to encode catalog data",
			err.Error(),
		)
		return "", diagnostics
	}
	return string(payload), diagnostics
}

func marshalObjectToJSON(attributeName string, data map[string]any) (string, diag.Diagnostics) {
	diagnostics := diag.Diagnostics{}
	payload, err := json.Marshal(data)
	if err != nil {
		diagnostics.AddAttributeError(
			path.Root(attributeName),
			"Failed to encode catalog data",
			err.Error(),
		)
		return "", diagnostics
	}
	return string(payload), diagnostics
}

func marshalAnyToJSON(attributeName string, data []map[string]any) (string, diag.Diagnostics) {
	return marshalCatalogToJSON(attributeName, data)
}

func marshalAnySliceToJSON(attributeName string, data []map[string]any) (string, diag.Diagnostics) {
	return marshalCatalogToJSON(attributeName, data)
}
