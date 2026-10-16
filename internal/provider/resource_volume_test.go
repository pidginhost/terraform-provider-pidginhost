package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccVolume(t *testing.T) {
	servers := testAccServerIDs(t, 2)
	product := testAccEnv(t, "TF_ACC_VOLUME_PRODUCT")
	size, err := strconv.Atoi(testAccEnv(t, "TF_ACC_VOLUME_SIZE"))
	if err != nil {
		t.Fatal(err)
	}
	alias := testAccName("vol")
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccVolumeConfig(servers[0], alias, product, size),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("alias"), knownvalue.StringExact(alias)),
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("server_id"), knownvalue.StringExact(servers[0])),
				},
			},
			{
				ResourceName:            "pidginhost_volume.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				// Grow and move to the second server in place.
				Config: testAccVolumeConfig(servers[1], alias, product, size+1),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_volume.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("size"), knownvalue.Int64Exact(int64(size+1))),
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("server_id"), knownvalue.StringExact(servers[1])),
				},
			},
		},
	})
}

func testAccVolumeConfig(server, alias, product string, size int) string {
	return fmt.Sprintf(`
resource "pidginhost_volume" "test" {
  server_id = %q
  alias     = %q
  product   = %q
  size      = %d
}
`, server, alias, product, size)
}

func testVolume(t *testing.T, r *volumeResource, size int64, server string) volumeModel {
	return volumeModel{
		ID:       types.StringValue("7"),
		ServerID: types.StringValue(server),
		Alias:    types.StringValue("data"),
		Size:     types.Int64Value(size),
		Product:  types.StringValue("ssd"),
		Project:  types.StringValue(""),
		Timeouts: nullTimeouts(t, testSchema(r)),
	}
}

func TestVolumeRefusesToShrink(t *testing.T) {
	r := &volumeResource{}
	s := testSchema(r)
	for _, tc := range []struct {
		size    int64
		refused bool
	}{{10, true}, {20, false}, {30, false}} {
		req := resource.ModifyPlanRequest{
			State: testState(t, s, testVolume(t, r, 20, "1")),
			Plan:  testPlan(t, s, testVolume(t, r, tc.size, "1")),
		}
		resp := resource.ModifyPlanResponse{Plan: req.Plan}
		r.ModifyPlan(context.Background(), req, &resp)
		if resp.Diagnostics.HasError() != tc.refused {
			t.Errorf("20 GB -> %d GB: refused = %v, want %v", tc.size, resp.Diagnostics.HasError(), tc.refused)
		}
	}
}

// fakeVolume serves volume 7, which takes two polls to finish a detach.
func fakeVolume(t *testing.T) (*pidginhost.APIClient, func() []string) {
	vol := pidginhost.Volume{Id: 7, Alias: pidginhost.PtrString("data"), Size: 20, Product: "ssd", Attached: true, Server: "web-1"}
	detaching := 0
	return fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/volumes/7/":
			if detaching > 0 {
				if detaching--; detaching == 0 {
					vol.Server = ""
				}
			}
			return 200, vol
		case "POST /api/cloud/volumes/7/detach/":
			vol.Attached, detaching = false, 2
			return 200, map[string]any{"detached": true}
		case "POST /api/cloud/volumes/7/attach/":
			if vol.Server != "" {
				return 400, map[string]any{"detail": "volume is attached"}
			}
			vol.Attached, vol.Server = true, fmt.Sprintf("web-%v", body["vm"])
			return 200, map[string]any{"attached": true}
		case "DELETE /api/cloud/volumes/7/":
			if vol.Server != "" {
				return 400, map[string]any{"detail": "volume is attached"}
			}
			return 204, nil
		case "GET /api/cloud/servers/2/":
			return 200, pidginhost.ServerDetail{Id: 2, Hostname: "web-2", Status: "active", Volumes: []pidginhost.Volume{vol}}
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
}

func TestVolumeMoveWaitsForTheDetach(t *testing.T) {
	client, calls := fakeVolume(t)
	r := &volumeResource{client: client}
	s := testSchema(r)
	req := resource.UpdateRequest{
		State: testState(t, s, testVolume(t, r, 20, "1")),
		Plan:  testPlan(t, s, testVolume(t, r, 20, "2")),
	}
	resp := resource.UpdateResponse{State: testState(t, s, nil)}
	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	var got volumeModel
	resp.State.Get(context.Background(), &got)
	if got.ServerID.ValueString() != "2" {
		t.Fatalf("server_id = %s, want 2", got.ServerID)
	}
	if !slices.Contains(calls(), "POST /api/cloud/volumes/7/attach/") {
		t.Fatalf("no attach call: %v", calls())
	}
}

func TestVolumeDeleteDetachesFirst(t *testing.T) {
	client, calls := fakeVolume(t)
	r := &volumeResource{client: client}
	s := testSchema(r)
	req := resource.DeleteRequest{State: testState(t, s, testVolume(t, r, 20, "1"))}
	resp := resource.DeleteResponse{}
	r.Delete(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	if !slices.Contains(calls(), "DELETE /api/cloud/volumes/7/") {
		t.Fatalf("no delete call: %v", calls())
	}
}

func TestVolumeReadFindsTheServerAfterImport(t *testing.T) {
	server := func(id int32, hostname string, volumes ...pidginhost.Volume) pidginhost.ServerDetail {
		return pidginhost.ServerDetail{Id: id, Hostname: hostname, Volumes: volumes, Status: "active"}
	}
	vol := pidginhost.Volume{Id: 7, Size: 20, Product: "ssd", Attached: true, Server: "web"}
	client, calls := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/volumes/7/":
			return 200, vol
		case "GET /api/cloud/servers/?page=1":
			list := []pidginhost.Server{{Id: 1, Hostname: pidginhost.PtrString("other")}, {Id: 2, Hostname: pidginhost.PtrString("web")}, {Id: 3, Hostname: pidginhost.PtrString("web")}}
			return 200, pidginhost.PaginatedServerList{Count: 3, Results: list}
		case "GET /api/cloud/servers/2/":
			return 200, server(2, "web")
		case "GET /api/cloud/servers/3/":
			return 200, server(3, "web", vol)
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &volumeResource{client: client}
	s := testSchema(r)
	imported := volumeModel{ID: types.StringValue("7"), Timeouts: nullTimeouts(t, s)}
	req := resource.ReadRequest{State: testState(t, s, imported)}
	resp := resource.ReadResponse{State: req.State}
	r.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("%v\ncalls: %v", resp.Diagnostics, calls())
	}
	var got volumeModel
	resp.State.Get(context.Background(), &got)
	if got.ServerID.ValueString() != "3" || got.Product.ValueString() != "ssd" {
		t.Fatalf("server_id = %s, product = %s; want 3, ssd", got.ServerID, got.Product)
	}
}
