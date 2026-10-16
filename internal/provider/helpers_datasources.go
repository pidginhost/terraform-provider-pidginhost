package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pidginhost "github.com/pidginhost/sdk-go"
)

// dataSourceBase hands a data source the API client.
type dataSourceBase struct{ client *pidginhost.APIClient }

func (d *dataSourceBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// listPage is what listAll needs from a paginated SDK list.
type listPage[T any] interface {
	GetResults() []T
	GetNext() string
}

// listAll walks every page of an SDK list call. The result is never nil, so
// an empty list encodes as [] and not null.
func listAll[T any, P listPage[T]](fetch func(page int32) (P, *http.Response, error)) ([]T, error) {
	items, err := fetchAll(func(page int32) ([]T, bool, error) {
		list, _, err := fetch(page)
		if err != nil {
			return nil, false, err
		}
		return list.GetResults(), list.GetNext() != "", nil
	})
	if err == nil && items == nil {
		items = []T{}
	}
	return items, err
}

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{Computed: true, Description: description}
}

func computedInt(description string) schema.Int64Attribute {
	return schema.Int64Attribute{Computed: true, Description: description}
}

func computedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Computed: true, Description: description}
}

// computedList is a computed list of objects with the given attributes.
func computedList(description string, attributes map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:     true,
		Description:  description,
		NestedObject: schema.NestedAttributeObject{Attributes: attributes},
	}
}
