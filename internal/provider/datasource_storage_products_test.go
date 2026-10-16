package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestStorageProductsDataSourceWalksEveryPage(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/storage-products/?page=1": `{"count": 2, "next": "more", "results": [
			{"id": 3, "slug": "fast-storage", "name": "Fast storage", "type": "storage", "unit": "GB", "price": "0.10", "min_size": 10, "max_size": 1000}]}`,
		"/api/cloud/storage-products/?page=2": `{"count": 2, "next": null, "results": [
			{"id": 4, "slug": "archive", "name": "Archive", "type": "storage", "unit": "GB", "price": "0.02", "min_size": 100, "max_size": 10000}]}`,
	})
	state, diags := readDataSource(t, NewStorageProductsDataSource(), client, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var products []storageProductModel
	if diags := state.GetAttribute(t.Context(), path.Root("storage_products"), &products); diags.HasError() {
		t.Fatal(diags)
	}
	if len(products) != 2 || products[0].ID != "3" || products[0].Price != "0.10" || products[1].MaxSize != 10000 {
		t.Fatalf("got %+v", products)
	}
}

func TestAccStorageProductsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `data "pidginhost_storage_products" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_storage_products.all", tfjsonpath.New("storage_products").AtSliceIndex(0).AtMapKey("slug"), knownvalue.NotNull()),
			},
		}},
	})
}
