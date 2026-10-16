package provider

import (
	"context"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ datasource.DataSourceWithConfigure = &imagesDataSource{}

func NewImagesDataSource() datasource.DataSource { return &imagesDataSource{} }

type imagesDataSource struct{ dataSourceBase }

type imageModel struct {
	ID              string  `tfsdk:"id"`
	Slug            string  `tfsdk:"slug"`
	Name            string  `tfsdk:"name"`
	FamilyName      *string `tfsdk:"family_name"`
	IsDefault       *bool   `tfsdk:"is_default"`
	DefaultUsername string  `tfsdk:"default_username"`
}

func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}

func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Operating system images available for new servers.",
		Attributes: map[string]schema.Attribute{
			"images": computedList("Every image.", map[string]schema.Attribute{
				"id":               computedString("Image id."),
				"slug":             computedString("Short name of the image, accepted wherever an image is selected."),
				"name":             computedString("Display name."),
				"family_name":      computedString("Operating system family, such as Ubuntu. Null when the image has none."),
				"is_default":       computedBool("Whether this is the default version within its family."),
				"default_username": computedString("Account the image provisions for SSH login."),
			}),
		},
	}
}

func (d *imagesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	images, err := listImages(ctx, d.client)
	if err != nil {
		resp.Diagnostics.Append(apiError("Listing images", err))
		return
	}
	models := make([]imageModel, len(images))
	for i, image := range images {
		models[i] = imageModel{
			ID:              strconv.Itoa(int(image.Id)),
			Slug:            image.Slug,
			Name:            image.Name,
			FamilyName:      image.FamilyName.Get(),
			IsDefault:       image.IsDefault,
			DefaultUsername: image.DefaultUsername,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		Images []imageModel `tfsdk:"images"`
	}{models})...)
}

func listImages(ctx context.Context, client *pidginhost.APIClient) ([]pidginhost.OSImage, error) {
	return listAll(func(page int32) (*pidginhost.PaginatedOSImageList, *http.Response, error) {
		return client.CloudAPI.CloudImagesList(ctx).Page(page).Execute()
	})
}
