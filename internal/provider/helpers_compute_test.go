package provider

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	pidginhost "github.com/pidginhost/sdk-go"
)

// fakeAPI starts a stand-in for the PidginHost API. handle receives each
// call as "METHOD /path[?query]" with its decoded JSON body and returns the
// status and the value to encode. It returns a client for it and a function
// that lists the calls made so far.
func fakeAPI(t *testing.T, handle func(call string, body map[string]any) (int, any)) (*pidginhost.APIClient, func() []string) {
	t.Helper()
	pollInterval = time.Millisecond
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			call += "?" + r.URL.RawQuery
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, call)
		status, out := handle(call, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if out != nil {
			_ = json.NewEncoder(w).Encode(out)
		}
	}))
	t.Cleanup(srv.Close)
	return newAPIClient("test", srv.URL, "test"), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func testSchema(r resource.Resource) schema.Schema {
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

// testState and testPlan build the state or plan a resource method receives.
func testState(t *testing.T, s schema.Schema, model any) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(context.Background()), nil)}
	if model != nil {
		if diags := st.Set(context.Background(), model); diags.HasError() {
			t.Fatal(diags)
		}
	}
	return st
}

func testPlan(t *testing.T, s schema.Schema, model any) tfsdk.Plan {
	t.Helper()
	st := testState(t, s, model)
	return tfsdk.Plan{Schema: s, Raw: st.Raw}
}

// nullTimeouts is an unset timeouts block of the schema s.
func nullTimeouts(t *testing.T, s schema.Schema) timeouts.Value {
	t.Helper()
	typ, diags := s.TypeAtPath(context.Background(), path.Root("timeouts"))
	if diags.HasError() {
		t.Fatal(diags)
	}
	return timeouts.Value{Object: types.ObjectNull(typ.(timeouts.Type).AttrTypes)}
}

// testAccServersConfig returns HCL for n servers, pidginhost_server.acc0 to
// acc<n-1>, for the acceptance tests that need servers to act on. Hostnames
// are prefix-0, prefix-1 and so on. It skips the test without TF_ACC, so
// callers read their settings only in acceptance runs. All are optional:
//
//	PIDGINHOST_ACC_IMAGE           image slug, default ubuntu24
//	PIDGINHOST_ACC_PACKAGE         package slug, default cloudv-1
//	PIDGINHOST_ACC_VOLUME_PRODUCT  storage product for TestAccVolume, default fast-storage
//	PIDGINHOST_ACC_VOLUME_SIZE     volume size in GB for TestAccVolume, default 10
func testAccServersConfig(t *testing.T, prefix string, n int) string {
	t.Helper()
	// Without TF_ACC resource.Test skips; skip before reading settings too.
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests run only with TF_ACC set")
	}
	image := cmp.Or(os.Getenv("PIDGINHOST_ACC_IMAGE"), "ubuntu24")
	pkg := cmp.Or(os.Getenv("PIDGINHOST_ACC_PACKAGE"), "cloudv-1")
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, `
resource "pidginhost_server" "acc%d" {
  hostname            = "%s-%d"
  image               = %q
  package             = %q
  password            = %q
  new_ipv4            = true
  destroy_public_ipv4 = true
}
`, i, prefix, i, image, pkg, "Tf1-"+acctest.RandString(20))
	}
	return b.String()
}

func TestSplitImportID(t *testing.T) {
	if parts, ok := splitImportID("4/123", 2); !ok || parts[0] != "4" || parts[1] != "123" {
		t.Fatalf("got %v, %v", parts, ok)
	}
	for _, bad := range []string{"123", "4/", "/123", "4/1/2"} {
		if _, ok := splitImportID(bad, 2); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
