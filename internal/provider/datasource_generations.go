package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

var _ datasource.DataSourceWithConfigure = &generationsDataSource{}

func NewGenerationsDataSource() datasource.DataSource { return &generationsDataSource{} }

type generationsDataSource struct{ dataSourceBase }

type generationModel struct {
	Slug             string  `tfsdk:"slug"`
	Name             string  `tfsdk:"name"`
	Description      *string `tfsdk:"description"`
	CPULabel         *string `tfsdk:"cpu_label"`
	PriceMultiplier  *string `tfsdk:"price_multiplier"`
	IsDefault        *bool   `tfsdk:"is_default"`
	FreeTierEligible *bool   `tfsdk:"free_tier_eligible"`
	DedicatedCPU     *bool   `tfsdk:"dedicated_cpu"`
}

func (d *generationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_generations"
}

func (d *generationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Hardware generations servers and Kubernetes clusters can run on.",
		Attributes: map[string]schema.Attribute{
			"generations": computedList("Every hardware generation.", map[string]schema.Attribute{
				"slug":               computedString("Short name of the generation, accepted wherever a generation is selected."),
				"name":               computedString("Display name."),
				"description":        computedString("Description of the hardware."),
				"cpu_label":          computedString("CPU model of the hosts."),
				"price_multiplier":   computedString("Multiplier applied to the base price, as a decimal string (1.00 = no change)."),
				"is_default":         computedBool("Whether the generation is used when none is selected."),
				"free_tier_eligible": computedBool("Whether free tier packages are available on this generation."),
				"dedicated_cpu":      computedBool("Whether vCPUs map 1:1 to host threads, without overcommit."),
			}),
		},
	}
}

func (d *generationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	generations, _, err := d.client.CloudAPI.CloudGenerationsList(ctx).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing hardware generations", err))
		return
	}
	models := make([]generationModel, len(generations))
	for i, g := range generations {
		models[i] = generationModel{
			Slug:             g.Slug,
			Name:             g.Name,
			Description:      g.Description,
			CPULabel:         g.CpuLabel,
			PriceMultiplier:  g.PriceMultiplier,
			IsDefault:        g.IsDefault,
			FreeTierEligible: g.FreeTierEligible,
			DedicatedCPU:     g.DedicatedCpu,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		Generations []generationModel `tfsdk:"generations"`
	}{models})...)
}
