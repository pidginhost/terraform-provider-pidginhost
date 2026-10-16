package provider

import (
	"context"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &storageProductsDataSource{}

func NewStorageProductsDataSource() datasource.DataSource { return &storageProductsDataSource{} }

type storageProductsDataSource struct{ dataSourceBase }

type storageProductModel struct {
	ID      string `tfsdk:"id"`
	Slug    string `tfsdk:"slug"`
	Name    string `tfsdk:"name"`
	Type    string `tfsdk:"type"`
	Unit    string `tfsdk:"unit"`
	Price   string `tfsdk:"price"`
	MinSize int32  `tfsdk:"min_size"`
	MaxSize int32  `tfsdk:"max_size"`
}

func (d *storageProductsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_products"
}

func (d *storageProductsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Storage products (volume tiers) available for new volumes.",
		Attributes: map[string]schema.Attribute{
			"storage_products": computedList("Every storage product.", map[string]schema.Attribute{
				"id":       computedString("Storage product id."),
				"slug":     computedString("Short name of the product, accepted as a volume's product."),
				"name":     computedString("Display name."),
				"type":     computedString("Kind of storage."),
				"unit":     computedString("Unit sizes and prices are counted in, such as GB."),
				"price":    computedString("Monthly price per unit, as a decimal string."),
				"min_size": computedInt("Smallest size, in units."),
				"max_size": computedInt("Largest size, in units."),
			}),
		},
	}
}

func (d *storageProductsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	products, err := listStorageProducts(ctx, d.client)
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing storage products", err))
		return
	}
	models := make([]storageProductModel, len(products))
	for i, p := range products {
		models[i] = storageProductModel{
			ID:      strconv.Itoa(int(p.Id)),
			Slug:    p.Slug,
			Name:    p.Name,
			Type:    p.Type,
			Unit:    p.Unit,
			Price:   p.Price,
			MinSize: p.MinSize,
			MaxSize: p.MaxSize,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		StorageProducts []storageProductModel `tfsdk:"storage_products"`
	}{models})...)
}

func listStorageProducts(ctx context.Context, client *pidginhost.APIClient) ([]pidginhost.StorageProduct, error) {
	return listAll(func(page int32) (*pidginhost.PaginatedStorageProductList, *http.Response, error) {
		return client.CloudAPI.CloudStorageProductsList(ctx).Page(page).Execute()
	})
}
