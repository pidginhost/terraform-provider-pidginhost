package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestImagesDataSourceWalksEveryPage(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/cloud/images/?page=1": `{"count": 2, "next": "more", "results": [
			{"id": 12, "slug": "ubuntu24", "name": "Ubuntu 24.04", "family_name": "Ubuntu", "is_default": true, "default_username": "ubuntu"}]}`,
		"/api/cloud/images/?page=2": `{"count": 2, "next": null, "results": [
			{"id": 13, "slug": "debian12", "name": "Debian 12", "family_name": null, "default_username": "debian"}]}`,
	})
	state, diags := readDataSource(t, NewImagesDataSource(), client, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var images []imageModel
	if diags := state.GetAttribute(t.Context(), path.Root("images"), &images); diags.HasError() {
		t.Fatal(diags)
	}
	if len(images) != 2 || images[0].ID != "12" || images[0].DefaultUsername != "ubuntu" || images[1].Slug != "debian12" {
		t.Fatalf("got %+v", images)
	}
	if images[1].FamilyName != nil || images[1].IsDefault != nil {
		t.Fatalf("missing values must stay null, got %+v", images[1])
	}
}

func TestAccImagesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `data "pidginhost_images" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.pidginhost_images.all", tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("slug"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.pidginhost_images.all", tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("default_username"), knownvalue.NotNull()),
			},
		}},
	})
}
