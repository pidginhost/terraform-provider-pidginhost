package provider

import (
	"context"
	"encoding/json"
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
	pidginhost "github.com/pidginhost/sdk-go"
)

// fakeAPI starts a stand-in for the PidginHost API. handle receives each
// call as "METHOD /path[?query]" with its decoded JSON body and returns the
// status and the value to encode. It returns a client for it and a function
// that lists the calls made so far.
func fakeAPI(t *testing.T, handle func(call string, body map[string]any) (int, any)) (*pidginhost.APIClient, func() []string) {
	t.Helper()
	computePollInterval = time.Millisecond
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

// testAccEnv reads a setting the compute acceptance tests need beyond the
// token: ids of existing servers and a storage product to use.
//
//	TF_ACC_SERVER_IDS      two or more ids of active servers, comma separated
//	TF_ACC_VOLUME_PRODUCT  storage product slug
//	TF_ACC_VOLUME_SIZE     a valid size in GB for that product
func testAccEnv(t *testing.T, name string) string {
	t.Helper()
	// Without TF_ACC resource.Test skips; skip before demanding settings too.
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests run only with TF_ACC set")
	}
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("acceptance test needs %s", name)
	}
	return value
}

// testAccServerIDs returns n ids from TF_ACC_SERVER_IDS.
func testAccServerIDs(t *testing.T, n int) []string {
	t.Helper()
	ids := strings.Split(testAccEnv(t, "TF_ACC_SERVER_IDS"), ",")
	if len(ids) < n {
		t.Fatalf("acceptance test needs %d ids in TF_ACC_SERVER_IDS", n)
	}
	return ids[:n]
}
