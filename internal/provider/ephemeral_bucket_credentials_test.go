package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccBucketCredentialsEphemeral(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: echoProviders,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "pidginhost_bucket" "test" {
  name     = %q
  quota_gb = 1
}

ephemeral "pidginhost_bucket_credentials" "test" {
  bucket_id = pidginhost_bucket.test.id
}

provider "echo" {
  data = length(ephemeral.pidginhost_bucket_credentials.test.secret_key) > 0
}

resource "echo" "test" {}
`, testAccName("ephemeral")),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data"), knownvalue.Bool(true)),
			},
		}},
	})
}

func TestBucketCredentialsEphemeralRevealsWithoutRotating(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		calls = append(calls, req.Method+" "+req.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"bucket":"c1-media","endpoint":"https://s3.example.com","region":"default",`+
			`"access_key":"current-access","secret_key":"current-secret"}`)
	}))
	defer srv.Close()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: echoProviders,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "pidginhost" {
  token    = "test"
  base_url = %q
}

ephemeral "pidginhost_bucket_credentials" "test" {
  bucket_id = "5"
}

provider "echo" {
  data = ephemeral.pidginhost_bucket_credentials.test
}

resource "echo" "test" {}
`, srv.URL),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data").AtMapKey("access_key"), knownvalue.StringExact("current-access")),
				statecheck.ExpectKnownValue("echo.test", tfjsonpath.New("data").AtMapKey("secret_key"), knownvalue.StringExact("current-secret")),
			},
		}},
	})
	mu.Lock()
	defer mu.Unlock()
	for _, call := range calls {
		if call != "POST /api/cloud/buckets/5/credentials/reveal/" {
			t.Fatalf("unexpected call %s", call)
		}
	}
	if len(calls) == 0 {
		t.Fatal("the keys were never read")
	}
}

func TestBucketCredentialsEphemeralErrorsNeverQuoteTheKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"s3cr3t-LEAK"}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	e := &bucketCredentialsEphemeral{client: newAPIClient("t", srv.URL, "test")}
	var schemaResp ephemeral.SchemaResponse
	e.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
	objectType := schemaResp.Schema.Type().TerraformType(ctx)
	config := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
		"bucket_id":  tftypes.NewValue(tftypes.String, "5"),
		"access_key": tftypes.NewValue(tftypes.String, nil),
		"secret_key": tftypes.NewValue(tftypes.String, nil),
	})}
	resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, nil)}}
	e.Open(ctx, ephemeral.OpenRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("no error")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Summary()+d.Detail(), "LEAK") {
			t.Fatalf("error quotes the response body: %q", d.Detail())
		}
	}
	if !resp.Result.Raw.IsNull() {
		t.Fatal("a failed open returned a result")
	}
}

func TestBucketCredentialsEphemeralRejectsEmptyResponses(t *testing.T) {
	for _, body := range []string{
		"", "null", `{"bucket":"test","endpoint":"https://s3.example.com","region":"default","access_key":"","secret_key":null}`,
	} {
		t.Run(fmt.Sprintf("body length %d", len(body)), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, body)
			}))
			defer srv.Close()
			e := &bucketCredentialsEphemeral{client: newAPIClient("test", srv.URL, "test")}
			var schemaResp ephemeral.SchemaResponse
			e.Schema(t.Context(), ephemeral.SchemaRequest{}, &schemaResp)
			typ := schemaResp.Schema.Type().TerraformType(t.Context())
			config := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(typ, map[string]tftypes.Value{
				"bucket_id": tftypes.NewValue(tftypes.String, "5"), "access_key": tftypes.NewValue(tftypes.String, nil),
				"secret_key": tftypes.NewValue(tftypes.String, nil),
			})}
			resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: schemaResp.Schema, Raw: tftypes.NewValue(typ, nil)}}
			e.Open(t.Context(), ephemeral.OpenRequest{Config: config}, &resp)
			if !resp.Diagnostics.HasError() || !resp.Result.Raw.IsNull() {
				t.Fatal("an empty credentials response must fail without returning a result")
			}
		})
	}
}
