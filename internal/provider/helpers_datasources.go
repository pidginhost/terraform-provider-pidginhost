package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

// onlyMatch returns the single item in matches. It fails when there is none,
// or when there are several, naming their ids so the user can pick one.
func onlyMatch[T any](matches []T, id func(T) int32, kind, query string) (T, error) {
	var zero T
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return zero, fmt.Errorf("no %s matches %s", kind, query)
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = strconv.Itoa(int(id(m)))
	}
	return zero, fmt.Errorf("%d %ss match %s (ids %s); look it up by id instead", len(matches), kind, query, strings.Join(ids, ", "))
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
