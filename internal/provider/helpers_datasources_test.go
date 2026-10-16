package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	pidginhost "github.com/pidginhost/sdk-go"
)

// fakeRoutesAPI answers each request whose path and query match a key of routes
// with that JSON body, and anything else with 404.
func fakeRoutesAPI(t *testing.T, routes map[string]string) *pidginhost.APIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return newAPIClient("test-token", srv.URL, "test")
}

// readDataSource runs Read on ds against client, with config holding the
// set arguments (every other attribute null), and returns the new state.
func readDataSource(t *testing.T, ds datasource.DataSource, client *pidginhost.APIClient, config map[string]tftypes.Value) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	ctx := t.Context()
	ds.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if diags := schemaResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	typ := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, attrType := range typ.AttributeTypes {
		values[name] = tftypes.NewValue(attrType, nil)
	}
	for name, value := range config {
		values[name] = value
	}
	raw := tftypes.NewValue(typ, values)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: raw}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, &resp)
	return resp.State, resp.Diagnostics
}
