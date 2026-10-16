package provider

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccVolume(t *testing.T) {
	alias := testAccName("vol")
	servers := testAccServersConfig(t, alias, 2)
	product := cmp.Or(os.Getenv("PIDGINHOST_ACC_VOLUME_PRODUCT"), "fast-storage")
	size, err := strconv.Atoi(cmp.Or(os.Getenv("PIDGINHOST_ACC_VOLUME_SIZE"), "10"))
	if err != nil {
		t.Fatal(err)
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: servers + testAccVolumeConfig("pidginhost_server.acc0.id", alias, product, size),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("alias"), knownvalue.StringExact(alias)),
					statecheck.CompareValuePairs("pidginhost_volume.test", tfjsonpath.New("server_id"), "pidginhost_server.acc0", tfjsonpath.New("id"), compare.ValuesSame()),
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
				Config: servers + testAccVolumeConfig("pidginhost_server.acc1.id", alias, product, size+1),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("pidginhost_volume.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_volume.test", tfjsonpath.New("size"), knownvalue.Int64Exact(int64(size+1))),
					statecheck.CompareValuePairs("pidginhost_volume.test", tfjsonpath.New("server_id"), "pidginhost_server.acc1", tfjsonpath.New("id"), compare.ValuesSame()),
				},
			},
		},
	})
}

// testAccVolumeConfig takes server as an expression, such as a reference.
func testAccVolumeConfig(server, alias, product string, size int) string {
	return fmt.Sprintf(`
resource "pidginhost_volume" "test" {
  server_id = %s
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
	vol := pidginhost.Volume{Id: 7, Alias: pidginhost.PtrString("data"), Size: 20, Product: "ssd", Attached: true, Server: "web-1", Status: "active"}
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
	vol := pidginhost.Volume{Id: 7, Size: 20, Product: "ssd", Attached: true, Server: "web", Status: "active"}
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

func TestVolumeGrowWaitsForActiveBeforeAndAfter(t *testing.T) {
	defer func(old time.Duration) { pollInterval = old }(pollInterval)
	pollInterval = time.Millisecond
	// A volume that is still provisioning, as right after creation or a move.
	vol := pidginhost.Volume{Id: 7, Alias: pidginhost.PtrString("data"), Size: 20, Product: "ssd", Attached: true, Server: "web-1", Status: "provisioning"}
	reads, resizeAt, refused := 0, 0, 0
	client, calls := fakeAPI(t, func(call string, body map[string]any) (int, any) {
		switch call {
		case "GET /api/cloud/volumes/7/":
			if reads++; reads == 2 {
				vol.Status = "active"
			}
			if resizeAt > 0 && reads >= resizeAt+2 {
				vol.Status, vol.Size = "active", int64(body2size)
			}
			return 200, vol
		case "PATCH /api/cloud/volumes/7/":
			// The API grows only active volumes and answers 409 otherwise.
			if vol.Status != "active" {
				refused++
				return 409, map[string]any{"detail": "The volume is provisioning."}
			}
			vol.Status, resizeAt = "upgrading", reads
			return 200, vol
		}
		return 500, map[string]any{"detail": "unexpected call " + call}
	})
	r := &volumeResource{client: client}
	s := testSchema(r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := resource.UpdateRequest{
		State: testState(t, s, testVolume(t, r, 20, "1")),
		Plan:  testPlan(t, s, testVolume(t, r, body2size, "1")),
	}
	resp := resource.UpdateResponse{State: testState(t, s, nil)}
	r.Update(ctx, req, &resp)
	if resp.Diagnostics.HasError() || refused > 0 {
		t.Fatalf("diags %v, refused %d, calls %v", resp.Diagnostics, refused, calls())
	}
	var got volumeModel
	resp.State.Get(context.Background(), &got)
	if got.Size.ValueInt64() != body2size || got.Status.ValueString() != "active" {
		t.Fatalf("state size %v status %v, calls %v", got.Size, got.Status, calls())
	}
}

// body2size is the size the grow test asks for.
const body2size = 30
