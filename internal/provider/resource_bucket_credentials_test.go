package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccBucketCredentials(t *testing.T) {
	name := testAccName("creds")
	const first, second = "pidginhost_bucket_credentials.first", "pidginhost_bucket_credentials.second"
	secret := tfjsonpath.New("secret_key")
	rotated := statecheck.CompareValue(compare.ValuesDiffer())
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketCredentialsConfig(name, "2026-01"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(first, tfjsonpath.New("access_key"), knownvalue.NotNull()),
					// Creating reads the keys without rotating them, so both hold the same pair.
					statecheck.CompareValuePairs(first, secret, second, secret, compare.ValuesSame()),
					rotated.AddStateValue(first, secret),
				},
			},
			{
				Config: testAccBucketCredentialsConfig(name, "2026-02"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(first, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{rotated.AddStateValue(first, secret)},
			},
		},
	})
}

func testAccBucketCredentialsConfig(name, rotation string) string {
	return fmt.Sprintf(`
resource "pidginhost_bucket" "test" {
  name     = %q
  quota_gb = 1
}

resource "pidginhost_bucket_credentials" "first" {
  bucket_id         = pidginhost_bucket.test.id
  rotation_triggers = { rotation = %q }
}

resource "pidginhost_bucket_credentials" "second" {
  bucket_id  = pidginhost_bucket.test.id
  depends_on = [pidginhost_bucket_credentials.first]
}
`, name, rotation)
}

// The fake API names the keys after the call that produced them.
func TestBucketCredentialsRevealOnCreateRotateOnTriggerChange(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		action := "reveal"
		if strings.HasSuffix(req.URL.Path, "/rotate/") {
			action = "rotate"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"bucket":"c1-media","endpoint":"https://s3.example.com","region":"default",`+
			`"access_key":"%s-access","secret_key":"%s-secret"}`, action, action)
	}))
	defer srv.Close()
	ctx := context.Background()
	r := &bucketCredentialsResource{client: newAPIClient("t", srv.URL, "test")}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	triggers := func(v string) types.Map {
		return types.MapValueMust(types.StringType, map[string]attr.Value{"rotation": types.StringValue(v)})
	}

	// Create reveals the current keys.
	plan := tfsdk.Plan{Schema: s}
	mustSet(t, plan.Set(ctx, bucketCredentialsModel{
		ID: types.StringUnknown(), BucketID: types.StringValue("5"), RotationTriggers: triggers("a"),
		AccessKey: types.StringUnknown(), SecretKey: types.StringUnknown(),
	}))
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	state := credentialsFrom(t, created.State.Get, created.Diagnostics.Errors())
	if state.ID.ValueString() != "5" || state.SecretKey.ValueString() != "reveal-secret" {
		t.Fatalf("create stored %+v", state)
	}

	// The same triggers plan the stored keys; new triggers plan unknown keys.
	for _, tc := range []struct {
		triggers string
		known    bool
	}{{"a", true}, {"b", false}} {
		next := state
		next.RotationTriggers = triggers(tc.triggers)
		planned := tfsdk.Plan{Schema: s}
		mustSet(t, planned.Set(ctx, next))
		modified := resource.ModifyPlanResponse{Plan: planned}
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: created.State, Plan: planned}, &modified)
		got := credentialsFrom(t, modified.Plan.Get, modified.Diagnostics.Errors())
		if got.SecretKey.IsUnknown() == tc.known || got.AccessKey.IsUnknown() == tc.known {
			t.Fatalf("triggers %q planned %+v", tc.triggers, got)
		}
	}

	// Update rotates only when the triggers changed.
	for _, tc := range []struct{ triggers, secret string }{{"a", "reveal-secret"}, {"b", "rotate-secret"}} {
		next := state
		next.RotationTriggers = triggers(tc.triggers)
		planned := tfsdk.Plan{Schema: s}
		mustSet(t, planned.Set(ctx, next))
		updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: planned, State: created.State}, &updated)
		if got := credentialsFrom(t, updated.State.Get, updated.Diagnostics.Errors()); got.SecretKey.ValueString() != tc.secret {
			t.Fatalf("triggers %q stored %+v", tc.triggers, got)
		}
	}

	want := "POST /api/cloud/buckets/5/credentials/reveal/,POST /api/cloud/buckets/5/credentials/rotate/"
	if got := strings.Join(calls, ","); got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}
}

func mustSet(t *testing.T, diags diag.Diagnostics) {
	t.Helper()
	if diags.HasError() {
		t.Fatalf("%v", diags)
	}
}

func credentialsFrom(t *testing.T, get func(context.Context, any) diag.Diagnostics, errs diag.Diagnostics) bucketCredentialsModel {
	t.Helper()
	if len(errs) > 0 {
		t.Fatalf("%v", errs)
	}
	var m bucketCredentialsModel
	if diags := get(context.Background(), &m); diags.HasError() {
		t.Fatalf("%v", diags)
	}
	return m
}

func TestBucketCredentialsErrorsNeverQuoteTheKeys(t *testing.T) {
	// A 200 the SDK cannot decode (bucket missing) still carries live keys.
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusOK, `{"access_key":"AKIA-LEAK","secret_key":"s3cr3t-LEAK"}`},
		{http.StatusBadRequest, `{"detail":"s3cr3t-LEAK"}`},
		// JSON decoding can quote a numeric value from a malformed response.
		{http.StatusOK, `{"secret_key":12345678901234567890e9999}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		ctx := context.Background()
		r := &bucketCredentialsResource{client: newAPIClient("t", srv.URL, "test")}
		var schemaResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		mustSet(t, plan.Set(ctx, bucketCredentialsModel{
			ID: types.StringUnknown(), BucketID: types.StringValue("5"), RotationTriggers: types.MapNull(types.StringType),
			AccessKey: types.StringUnknown(), SecretKey: types.StringUnknown(),
		}))
		created := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
		srv.Close()
		if !created.Diagnostics.HasError() {
			t.Fatalf("status %d: expected an error", tc.status)
		}
		for _, d := range created.Diagnostics {
			if strings.Contains(d.Summary()+d.Detail(), "LEAK") || strings.Contains(d.Detail(), "12345678901234567890") {
				t.Fatalf("status %d: error quotes the response body", tc.status)
			}
		}
	}
}
