package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ datasource.DataSourceWithConfigure        = &sshKeyDataSource{}
	_ datasource.DataSourceWithConfigValidators = &sshKeyDataSource{}
)

func NewSSHKeyDataSource() datasource.DataSource { return &sshKeyDataSource{} }

type sshKeyDataSource struct{ dataSourceBase }

func (d *sshKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *sshKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up one SSH key stored on the account by id, alias or fingerprint. Set exactly one of them; " +
			"the lookup fails when no key or more than one key matches.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Id of the key.",
			},
			"alias": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Alias of the key. Empty when the key has none.",
			},
			"fingerprint": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Fingerprint of the key, as the API reports it (SHA256:...).",
			},
			"key": computedString("Public key in OpenSSH format."),
		},
	}
}

func (d *sshKeyDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("alias"), path.MatchRoot("fingerprint")),
	}
}

func (d *sshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config sshKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := listAll(func(page int32) (*pidginhost.PaginatedSSHKeyList, *http.Response, error) {
		return d.client.AccountAPI.AccountSshKeysList(ctx).Page(page).Execute()
	})
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing SSH keys", err))
		return
	}
	attribute, value := "fingerprint", config.Fingerprint
	if !config.ID.IsNull() {
		attribute, value = "id", config.ID
	} else if !config.Alias.IsNull() {
		attribute, value = "alias", config.Alias
	}
	key, err := findSSHKey(keys, attribute, value.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Looking up SSH key", err.Error())
		return
	}
	var state sshKeyModel
	state.fill(&key)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// findSSHKey returns the one key whose attribute (id, alias or fingerprint)
// equals value.
func findSSHKey(keys []pidginhost.SSHKey, attribute, value string) (pidginhost.SSHKey, error) {
	var matches []pidginhost.SSHKey
	for _, k := range keys {
		got := k.Fingerprint
		switch attribute {
		case "id":
			got = strconv.Itoa(int(k.Id))
		case "alias":
			got = k.GetAlias()
		}
		if got == value {
			matches = append(matches, k)
		}
	}
	return onlyMatch(matches, func(k pidginhost.SSHKey) int32 { return k.Id }, "SSH key", fmt.Sprintf("%s %q", attribute, value))
}
