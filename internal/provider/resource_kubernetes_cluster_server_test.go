package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// Connecting reboots the server, and the cluster needs cloud VM access, which
// the API can toggle but not read; so the test takes a prepared cluster and server.
func TestAccKubernetesClusterServer(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	serverID := os.Getenv("PIDGINHOST_ACC_KUBERNETES_SERVER_ID")
	if serverID == "" {
		t.Skip("set PIDGINHOST_ACC_KUBERNETES_SERVER_ID to an active server the test may reboot")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "pidginhost_kubernetes_cluster_server" "test" {
  cluster_id = %q
  server_id  = %q
}
`, clusterID, serverID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster_server.test", tfjsonpath.New("id"), knownvalue.StringExact(clusterID+"/"+serverID)),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster_server.test", tfjsonpath.New("ip"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "pidginhost_kubernetes_cluster_server.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// fakeConnections serves the connected and eligible servers of cluster 7 and
// records connect and disconnect requests. Requests take effect after a poll.
func fakeConnections(connected bool, eligible string) (*requestLog, http.Handler) {
	var log requestLog
	var state atomic.Bool
	state.Store(connected)
	var pending atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"connected-vms/", func(w http.ResponseWriter, _ *http.Request) {
		vms := ""
		if state.Load() {
			vms = `{"id": 12, "hostname": "web-1", "ip": "198.51.100.12"}`
		}
		if pending.Add(-1) == 0 {
			state.Store(!state.Load())
		}
		k8sReply(w, http.StatusOK, `{"vms": [`+vms+`]}`)
	})
	mux.HandleFunc("GET "+clusterPath+"eligible-vms/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, `{"vms": [`+eligible+`]}`)
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		pending.Store(2)
		k8sReply(w, http.StatusOK, `{"status": "queued", "message": "The server reboots."}`)
	})
	return &log, mux
}

func TestConnectServerPostsAndWaits(t *testing.T) {
	fastKubernetesPoll(t)
	log, handler := fakeConnections(false, `{"id": 12, "hostname": "web-1"}`)
	client := testKubernetesAPI(t, handler)
	ctx := context.Background()

	if err := connectServer(ctx, client, "7", 12); err != nil {
		t.Fatal(err)
	}
	vm, err := waitServerConnection(ctx, client, "7", 12, true)
	if err != nil || vm == nil || vm.Ip != "198.51.100.12" {
		t.Fatalf("vm %v, err %v", vm, err)
	}
	if want := "POST " + clusterPath + `connect-vm/ {"server":12}`; log.String() != want {
		t.Fatalf("writes %q", log.String())
	}
}

func TestConnectServerDoesNotRebootAConnectedServer(t *testing.T) {
	log, handler := fakeConnections(true, "")
	if err := connectServer(context.Background(), testKubernetesAPI(t, handler), "7", 12); err != nil || log.String() != "" {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestConnectServerExplainsAnIneligibleServer(t *testing.T) {
	log, handler := fakeConnections(false, `{"id": 13, "hostname": "db-1"}`)
	err := connectServer(context.Background(), testKubernetesAPI(t, handler), "7", 12)
	if err == nil || !strings.Contains(err.Error(), "cloud VM access") || log.String() != "" {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestDisconnectServerWaitsUntilItIsGone(t *testing.T) {
	fastKubernetesPoll(t)
	log, handler := fakeConnections(true, "")
	client := testKubernetesAPI(t, handler)

	if err := disconnectServer(context.Background(), client, "7", 12); err != nil {
		t.Fatal(err)
	}
	if want := "POST " + clusterPath + `disconnect-vm/ {"server":12}`; log.String() != want {
		t.Fatalf("writes %q", log.String())
	}
	if vm, err := connectedServer(context.Background(), client, "7", 12); vm != nil || err != nil {
		t.Fatalf("still connected: %v %v", vm, err)
	}
}
