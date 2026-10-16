package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccKubernetesRoute(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	name := testAccName("route")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccKubernetesRouteConfig(clusterID, name, 8443, 443),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_route.test", tfjsonpath.New("namespace"), knownvalue.StringExact("default")),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_route.test", tfjsonpath.New("backend_namespace"), knownvalue.StringExact("default")),
				},
			},
			{
				ResourceName:      "pidginhost_kubernetes_route.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs := s.RootModule().Resources["pidginhost_kubernetes_route.test"]
					return rs.Primary.Attributes["cluster_id"] + "/tcp/" + rs.Primary.ID, nil
				},
				// The gateway may report the route ready between the two reads.
				ImportStateVerifyIgnore: []string{"status_ready", "status_message"},
			},
			{
				Config: testAccKubernetesRouteConfig(clusterID, name, 8444, 8443),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_route.test", tfjsonpath.New("port"), knownvalue.Int64Exact(8444)),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_route.test", tfjsonpath.New("backend_service_port"), knownvalue.Int64Exact(8443)),
				},
			},
		},
	})
}

// testAccKubernetesClusterID returns the existing cluster the tests of the
// objects inside a cluster use, so they do not each provision a billed one.
func testAccKubernetesClusterID(t *testing.T) string {
	t.Helper()
	id := os.Getenv("PIDGINHOST_ACC_KUBERNETES_CLUSTER_ID")
	if id == "" {
		t.Skip("set PIDGINHOST_ACC_KUBERNETES_CLUSTER_ID to an active cluster to run this test")
	}
	return id
}

func testAccKubernetesRouteConfig(clusterID, name string, port, backendPort int) string {
	return fmt.Sprintf(`
resource "pidginhost_kubernetes_route" "test" {
  cluster_id           = %q
  protocol             = "tcp"
  name                 = %q
  port                 = %d
  backend_service_name = "web"
  backend_service_port = %d
}
`, clusterID, name, port, backendPort)
}

func TestRouteConfigProblems(t *testing.T) {
	hostnames := types.SetValueMust(types.StringType, nil)
	for _, tc := range []struct {
		name  string
		model kubernetesRouteModel
		want  []string
	}{
		{"http ok", kubernetesRouteModel{Protocol: types.StringValue("http"), Hostnames: hostnames}, nil},
		{"http needs hostnames, refuses port", kubernetesRouteModel{Protocol: types.StringValue("http"), Port: types.Int64Value(80)}, []string{"hostnames", "port"}},
		{"tcp ok", kubernetesRouteModel{Protocol: types.StringValue("tcp"), Port: types.Int64Value(8443)}, nil},
		{"udp needs port, refuses http fields", kubernetesRouteModel{
			Protocol: types.StringValue("udp"), Hostnames: hostnames, PathPrefix: types.StringValue("/"), EnableTLS: types.BoolUnknown(),
		}, []string{"enable_tls", "hostnames", "path_prefix", "port"}},
		{"unknown protocol is not checked", kubernetesRouteModel{Protocol: types.StringUnknown(), Port: types.Int64Value(80)}, nil},
	} {
		var got []string
		for name := range routeConfigProblems(tc.model) {
			got = append(got, name)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRouteFillKeepsEachProtocolsFields(t *testing.T) {
	var m kubernetesRouteModel
	ready := true
	m.fill(&pidginhost.HTTPRoute{
		Id: 4, Name: "web", Hostnames: []string{"example.com"}, BackendServiceName: "web", BackendServicePort: 80,
		PathPrefix: pidginhost.PtrString("/"), EnableTls: pidginhost.PtrBool(true), StatusReady: *pidginhost.NewNullableBool(&ready),
	})
	if m.ID.ValueString() != "4" || m.Namespace.ValueString() != "default" || len(m.Hostnames.Elements()) != 1 ||
		m.PathPrefix.ValueString() != "/" || !m.Port.IsNull() || !m.StatusReady.ValueBool() {
		t.Fatalf("http route: %+v", m)
	}

	m.fill(&pidginhost.TCPRoute{Id: 5, Name: "dns", Namespace: pidginhost.PtrString("infra"), Port: 5353, BackendServicePort: 53})
	if m.Port.ValueInt64() != 5353 || m.Namespace.ValueString() != "infra" || !m.Hostnames.IsNull() ||
		!m.PathPrefix.IsNull() || !m.EnableTLS.IsNull() || !m.StatusReady.IsNull() {
		t.Fatalf("tcp route: %+v", m)
	}
}

func TestSplitImportID(t *testing.T) {
	if parts, ok := splitImportID("7/tcp/12", 3); !ok || parts[2] != "12" {
		t.Fatalf("got %v %v", parts, ok)
	}
	for _, id := range []string{"7/12", "7//12", "7/tcp/12/1"} {
		if _, ok := splitImportID(id, 3); ok {
			t.Errorf("%q was accepted", id)
		}
	}
	if _, err := int32ID("x7"); err == nil {
		t.Error("x7 was accepted as an ID")
	}
}

const platformRouteJSON = `{"id": 1, "name": "kube-apiserver", "namespace": "envoy-gw", "port": 0,
	"backend_service_name": "kube-apiserver", "backend_service_port": 6443, "backend_namespace": "default",
	"status_ready": true, "status_message": "", "created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z"}`

func TestRouteRefusesThePlatformRoute(t *testing.T) {
	var log requestLog
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"tcproutes/1/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, platformRouteJSON)
	})
	mux.HandleFunc("DELETE /", func(w http.ResponseWriter, r *http.Request) { log.add(r) })
	r := &kubernetesRouteResource{client: testKubernetesAPI(t, mux)}
	ctx := context.Background()

	// An import reads the route first.
	imported := kubernetesRouteModel{ID: types.StringValue("1"), ClusterID: types.StringValue("7"), Protocol: types.StringValue("tcp")}
	readResp := fwresource.ReadResponse{State: testRouteState(t, kubernetesRouteModel{})}
	r.Read(ctx, fwresource.ReadRequest{State: testRouteState(t, imported)}, &readResp)
	if errs := readResp.Diagnostics.Errors(); len(errs) != 1 || errs[0].Summary() != "Platform route" {
		t.Fatalf("the platform route was read into state: %v", readResp.Diagnostics)
	}

	adopted := imported
	adopted.Name, adopted.Namespace = types.StringValue("kube-apiserver"), types.StringValue("envoy-gw")
	var deleteResp fwresource.DeleteResponse
	r.Delete(ctx, fwresource.DeleteRequest{State: testRouteState(t, adopted)}, &deleteResp)
	if errs := deleteResp.Diagnostics.Errors(); len(errs) != 1 || errs[0].Summary() != "Platform route" || log.String() != "" {
		t.Fatalf("the platform route was deleted: %v %q", deleteResp.Diagnostics, log.String())
	}
}

func testRouteState(t *testing.T, m kubernetesRouteModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var schemaResp fwresource.SchemaResponse
	NewKubernetesRouteResource().Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	if m.Hostnames.ElementType(ctx) == nil {
		m.Hostnames = types.SetNull(types.StringType)
	}
	if diags := state.Set(ctx, m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}
