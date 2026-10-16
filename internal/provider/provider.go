package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ provider.Provider = &pidginhostProvider{}

// tokenEnvVars are read in order when the configuration has no token.
var tokenEnvVars = []string{"PIDGINHOST_TOKEN", "PIDGINHOST_ACCESS_TOKEN", "PIDGINHOST_API_TOKEN"}

// New returns the provider factory for the given release version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &pidginhostProvider{version: version}
	}
}

type pidginhostProvider struct {
	version string
}

type providerModel struct {
	Token   types.String `tfsdk:"token"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *pidginhostProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pidginhost"
	resp.Version = p.version
}

func (p *pidginhostProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage PidginHost cloud servers, storage, networking, DNS and Kubernetes.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "PidginHost API token. Defaults to the first of the PIDGINHOST_TOKEN, " +
					"PIDGINHOST_ACCESS_TOKEN and PIDGINHOST_API_TOKEN environment variables that is set.",
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "API base URL. Defaults to PIDGINHOST_BASE_URL, then " + defaultBaseURL + ".",
			},
		},
	}
}

func (p *pidginhostProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Token.IsUnknown() || config.BaseURL.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration",
			"token and base_url must be known before the plan; they cannot depend on values computed during apply.")
		return
	}

	token := config.Token.ValueString()
	for _, name := range tokenEnvVars {
		if token != "" {
			break
		}
		token = os.Getenv(name)
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing API token",
			"Set token in the provider block or export PIDGINHOST_TOKEN.")
		return
	}

	baseURL := config.BaseURL.ValueString()
	if baseURL == "" {
		baseURL = os.Getenv("PIDGINHOST_BASE_URL")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	userAgent := fmt.Sprintf("terraform-provider-pidginhost/%s Terraform/%s", p.version, req.TerraformVersion)
	client := newAPIClient(token, baseURL, userAgent)
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *pidginhostProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSSHKeyResource,
	}
}

func (p *pidginhostProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewImagesDataSource,
		NewPackagesDataSource,
		NewGenerationsDataSource,
		NewStorageProductsDataSource,
	}
}

// clientFrom unpacks the provider data handed to a resource or data source.
// It returns nil while the provider is still unconfigured (validation runs).
func clientFrom(data any, diags interface{ AddError(string, string) }) *pidginhost.APIClient {
	if data == nil {
		return nil
	}
	client, ok := data.(*pidginhost.APIClient)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *pidginhost.APIClient, got %T", data))
		return nil
	}
	return client
}
