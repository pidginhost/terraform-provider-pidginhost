package provider

import (
	"context"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &packagesDataSource{}

func NewPackagesDataSource() datasource.DataSource { return &packagesDataSource{} }

type packagesDataSource struct{ dataSourceBase }

type packageModel struct {
	ID                   string   `tfsdk:"id"`
	Slug                 string   `tfsdk:"slug"`
	Name                 string   `tfsdk:"name"`
	Cpus                 int32    `tfsdk:"cpus"`
	Memory               int32    `tfsdk:"memory"`
	DiskSize             int32    `tfsdk:"disk_size"`
	Traffic              int32    `tfsdk:"traffic"`
	AvailableGenerations []string `tfsdk:"available_generations"`
}

func (d *packagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_packages"
}

func (d *packagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Server packages (sizes) available for new servers.",
		Attributes: map[string]schema.Attribute{
			"generation": schema.StringAttribute{
				Optional: true,
				Description: "Only list packages available on this hardware generation (its slug). Free tier packages " +
					"are left out when the generation is not free tier eligible.",
			},
			"packages": computedList("Every matching package.", map[string]schema.Attribute{
				"id":        computedString("Package id."),
				"slug":      computedString("Short name of the package, accepted wherever a package is selected."),
				"name":      computedString("Display name."),
				"cpus":      computedInt("Number of vCPUs."),
				"memory":    computedInt("Memory, in GB."),
				"disk_size": computedInt("Disk size, in GB."),
				"traffic":   computedInt("Included monthly traffic, in TB."),
				"available_generations": schema.ListAttribute{
					Computed:    true,
					ElementType: types.StringType,
					Description: "Slugs of the hardware generations that offer the package.",
				},
			}),
		},
	}
}

func (d *packagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var generation types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("generation"), &generation)...)
	if resp.Diagnostics.HasError() {
		return
	}
	packages, err := listPackages(ctx, d.client, generation.ValueString())
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing server packages", err))
		return
	}
	models := make([]packageModel, len(packages))
	for i, p := range packages {
		models[i] = packageModel{
			ID:                   strconv.Itoa(int(p.Id)),
			Slug:                 p.Slug,
			Name:                 p.Name,
			Cpus:                 p.Cpus,
			Memory:               p.Memory,
			DiskSize:             p.DiskSize,
			Traffic:              p.Traffic,
			AvailableGenerations: p.AvailableGenerations,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		Generation types.String   `tfsdk:"generation"`
		Packages   []packageModel `tfsdk:"packages"`
	}{generation, models})...)
}

// listPackages lists every server package, or only those of one hardware
// generation when generation is set.
func listPackages(ctx context.Context, client *pidginhost.APIClient, generation string) ([]pidginhost.ServerProduct, error) {
	return listAll(func(page int32) (*pidginhost.PaginatedServerProductList, *http.Response, error) {
		req := client.CloudAPI.CloudServerPackagesList(ctx).Page(page)
		if generation != "" {
			req = req.Generation(generation)
		}
		return req.Execute()
	})
}
