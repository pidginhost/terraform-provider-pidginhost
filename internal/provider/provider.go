package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ provider.Provider = &pidginhostProvider{}

	providerVersion = "dev"
)

// SetVersion updates the provider version reported via metadata. This should
// be called by binaries embedding the provider before serving.
func SetVersion(v string) {
	if v == "" {
		return
	}
	providerVersion = v
}

func New() provider.Provider {
	return &pidginhostProvider{}
}

type pidginhostProvider struct{}

type providerData struct {
	Token   types.String `tfsdk:"token"`
	BaseURL types.String `tfsdk:"base_url"`
}

type Client struct {
	API API
}

func (p *pidginhostProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pidginhost"
	resp.Version = providerVersion
}

func (p *pidginhostProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "PidginHost API token. May also be supplied via the PIDGINHOST_TOKEN environment variable.",
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL for the PidginHost API (defaults to https://www.pidginhost.com).",
			},
		},
	}
}

func (p *pidginhostProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerData

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := ""
	if !config.Token.IsNull() && !config.Token.IsUnknown() {
		token = config.Token.ValueString()
	}
	if token == "" {
		token = os.Getenv("PIDGINHOST_TOKEN")
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing API Token",
			"The PidginHost API token must be provided either via the provider configuration or the PIDGINHOST_TOKEN environment variable.",
		)
		return
	}

	baseURL := "https://www.pidginhost.com"
	if !config.BaseURL.IsNull() && !config.BaseURL.IsUnknown() {
		baseURL = config.BaseURL.ValueString()
	}

	client := &Client{
		API: NewAPIClient(token, baseURL),
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *pidginhostProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewServerResource,
	}
}

func (p *pidginhostProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCatalogDataSource,
	}
}
