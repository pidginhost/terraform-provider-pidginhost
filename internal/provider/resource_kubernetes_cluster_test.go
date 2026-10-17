package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccKubernetesCluster(t *testing.T) {
	pkg := testAccKubernetesPackage(t)
	name := testAccName("k8s")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccKubernetesClusterConfig(name, pkg),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster.test", tfjsonpath.New("status"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster.test", tfjsonpath.New("protected"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster.test", tfjsonpath.New("encryption"), knownvalue.StringExact("disabled")),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster.test", tfjsonpath.New("kube_version"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "pidginhost_kubernetes_cluster.test",
				ImportState:       true,
				ImportStateVerify: true,
				// The API does not return the first pool settings.
				ImportStateVerifyIgnore: []string{"resource_pool_package", "resource_pool_size", "enable_gateway_api"},
			},
			{
				Config: testAccKubernetesClusterConfig(name+"-renamed", pkg),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_cluster.test", tfjsonpath.New("name"), knownvalue.StringExact(name+"-renamed")),
				},
			},
		},
	})
}

// testAccKubernetesPackage gates the test that creates a billed cluster and
// returns the worker package to build it with.
func testAccKubernetesPackage(t *testing.T) string {
	t.Helper()
	if os.Getenv("PIDGINHOST_ACC_KUBERNETES") != "1" {
		t.Skip("set PIDGINHOST_ACC_KUBERNETES=1 to run tests that create a billed Kubernetes cluster")
	}
	pkg := os.Getenv("PIDGINHOST_ACC_KUBERNETES_PACKAGE")
	if pkg == "" {
		t.Fatal("set PIDGINHOST_ACC_KUBERNETES_PACKAGE to the smallest worker package of the dev cluster type")
	}
	return pkg
}

func testAccKubernetesClusterConfig(name, pkg string) string {
	return fmt.Sprintf(`
resource "pidginhost_kubernetes_cluster" "test" {
  name                  = %q
  cluster_type          = "dev"
  resource_pool_package = %q
  resource_pool_size    = 1
  protected             = false
}
`, name, pkg)
}

// fastKubernetesPoll makes the waits of a test poll without pausing.
func fastKubernetesPoll(t *testing.T) {
	t.Helper()
	old := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = old })
}

// testKubernetesAPI serves handler as a fake API and returns a client for it.
func testKubernetesAPI(t *testing.T, handler http.Handler) *pidginhost.APIClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newAPIClient("t", srv.URL, "test")
}

func k8sReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// requestLog records the writes a fake API received, as "METHOD path body".
type requestLog struct {
	mu     sync.Mutex
	writes []string
}

func (l *requestLog) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
}

func (l *requestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.writes, "\n")
}

const clusterPath = "/api/kubernetes/clusters/7/"

func clusterJSON(status, version string, featuresReady, protected bool) string {
	return fmt.Sprintf(`{"id": 7, "status": %q, "name": "test", "generation": "g1", "cluster_type": "dev",
		"kube_version": %q, "price_per_month": "10.00", "price_per_hour": 0.01, "features": [],
		"features_ready": %t, "kubeconfig_valid_until": null, "ipv4_address": "192.0.2.20",
		"ipv6_address": "2001:db8::20", "dual_stack": false, "protected": %t, "talos_version": "1.10.0",
		"talos_upgrade_available": false, "talos_next_version": null, "storage_quota_gb": null,
		"last_pool_used_bytes": null, "last_storage_sync_at": null}`, status, version, featuresReady, protected)
}

func encryptionJSON(mode, status, operation string) string {
	return fmt.Sprintf(`{"mode": %q, "status": %q, "changed_at": null, "verified_at": null,
		"restart_required": false, "restart_required_at": null, "restart_checked_at": null,
		"stale_pod_count": null, "reason": "", "error": "", "per_node": {}, "operation": %s}`, mode, status, operation)
}

func encryptionOperationJSON(status, reason, message string) string {
	return fmt.Sprintf(`{"id": 4, "kind": "toggle", "status": %q, "requested_mode": "wireguard",
		"previous_mode": "none", "reason": %q, "message": %q, "override_unverifiable": false,
		"request_id": "r1", "created_at": "2026-01-01T00:00:00Z", "finished_at": null}`, status, reason, message)
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.35.7", "1.35.7", 0},
		{"1.34.10", "1.35.7", -1},
		{"1.36.3", "1.35.7", 1},
		{"1.35.10", "1.35.9", 1},
		{"1.35", "1.35.0", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUpgradeKubeVersionStepsToTheTarget(t *testing.T) {
	fastKubernetesPoll(t)
	versions := []string{"1.34.10", "1.35.7", "1.36.3"}
	var step, posts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("active", versions[step.Load()], true, false))
	})
	mux.HandleFunc("POST "+clusterPath+"kube-version-upgrade/", func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		step.Add(1)
		k8sReply(w, http.StatusOK, `{"status": "started"}`)
	})

	err := upgradeKubeVersion(context.Background(), testKubernetesAPI(t, mux), "7", "1.36.3")
	if err != nil || posts.Load() != 2 {
		t.Fatalf("err %v, %d upgrade requests, want 2", err, posts.Load())
	}
}

func TestUpgradeKubeVersionFailsWhenTheVersionStays(t *testing.T) {
	fastKubernetesPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, false))
	})
	mux.HandleFunc("POST "+clusterPath+"kube-version-upgrade/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, `{"status": "started"}`)
	})

	err := upgradeKubeVersion(context.Background(), testKubernetesAPI(t, mux), "7", "1.36.3")
	if err == nil || !strings.Contains(err.Error(), "did not change") {
		t.Fatalf("got %v", err)
	}
}

func TestUpgradeKubeVersionRefusesADowngrade(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("active", "1.36.3", true, false))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		k8sReply(w, http.StatusOK, `{"status": "started"}`)
	})

	err := upgradeKubeVersion(context.Background(), testKubernetesAPI(t, mux), "7", "1.35.7")
	if err == nil || !strings.Contains(err.Error(), "downgrades") || log.String() != "" {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestUpgradeKubeVersionWaitsForAFailedClusterToFail(t *testing.T) {
	fastKubernetesPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("failed", "1.35.7", false, false))
	})

	err := upgradeKubeVersion(context.Background(), testKubernetesAPI(t, mux), "7", "1.36.3")
	if err == nil || !strings.Contains(err.Error(), "the cluster is failed") {
		t.Fatalf("got %v", err)
	}
}

func TestSetEncryptionDoesNothingWhenTheModeMatches(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, encryptionJSON("none", "disabled", "null"))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) { log.add(r) })

	if err := setEncryption(context.Background(), testKubernetesAPI(t, mux), "7", "none", false); err != nil || log.String() != "" {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestSetEncryptionWaitsForTheChange(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		switch {
		case log.String() == "":
			k8sReply(w, http.StatusOK, encryptionJSON("none", "disabled", "null"))
		case gets.Add(1) < 3:
			k8sReply(w, http.StatusOK, encryptionJSON("none", "enabling", encryptionOperationJSON("running", "", "")))
		default:
			k8sReply(w, http.StatusOK, encryptionJSON("wireguard", "enabled", encryptionOperationJSON("succeeded", "", "")))
		}
	})
	mux.HandleFunc("POST "+clusterPath+"encryption/", func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		k8sReply(w, http.StatusAccepted, encryptionOperationJSON("pending", "", ""))
	})

	err := setEncryption(context.Background(), testKubernetesAPI(t, mux), "7", "wireguard", true)
	want := `POST /api/kubernetes/clusters/7/encryption/ {"acknowledge_workload_restart":true,"mode":"wireguard"}`
	if err != nil || log.String() != want || gets.Load() < 3 {
		t.Fatalf("err %v, writes %q, polls %d", err, log.String(), gets.Load())
	}
}

func TestSetEncryptionReportsTheRefusalReason(t *testing.T) {
	fastKubernetesPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, encryptionJSON("none", "disabled", "null"))
	})
	mux.HandleFunc("POST "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusConflict, `{"message": "Another cluster operation is running.", "reason": "cluster_operation_in_progress"}`)
	})

	err := setEncryption(context.Background(), testKubernetesAPI(t, mux), "7", "wireguard", true)
	want := "the API refused the encryption change (cluster_operation_in_progress): Another cluster operation is running."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
}

func TestSetEncryptionFailsWhenTheOperationFails(t *testing.T) {
	fastKubernetesPoll(t)
	var posted atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		if !posted.Load() {
			k8sReply(w, http.StatusOK, encryptionJSON("none", "disabled", "null"))
			return
		}
		k8sReply(w, http.StatusOK, encryptionJSON("none", "disabled", encryptionOperationJSON("failed", "helm_upgrade_failed", "rollout timed out")))
	})
	mux.HandleFunc("POST "+clusterPath+"encryption/", func(w http.ResponseWriter, _ *http.Request) {
		posted.Store(true)
		k8sReply(w, http.StatusAccepted, encryptionOperationJSON("pending", "", ""))
	})

	err := setEncryption(context.Background(), testKubernetesAPI(t, mux), "7", "wireguard", true)
	if err == nil || err.Error() != "the encryption change failed (helm_upgrade_failed): rollout timed out" {
		t.Fatalf("got %v", err)
	}
}

func TestProvisionWaitsThroughPendingAndUnprotects(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		switch gets.Add(1) {
		case 1:
			k8sReply(w, http.StatusNotFound, `{"detail": "Not found."}`)
		case 2:
			k8sReply(w, http.StatusOK, clusterJSON("provisioning", "1.35.7", false, true))
		default:
			k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, true))
		}
	})
	mux.HandleFunc("PATCH "+clusterPath, func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, false))
	})

	r := &kubernetesClusterResource{client: testKubernetesAPI(t, mux)}
	err := r.provision(context.Background(), "7", kubernetesClusterModel{Protected: types.BoolValue(false)})
	if want := `PATCH ` + clusterPath + ` {"protected":false}`; err != nil || log.String() != want {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestProvisionFailsForAFailedCluster(t *testing.T) {
	fastKubernetesPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("failed", "1.35.7", false, false))
	})

	r := &kubernetesClusterResource{client: testKubernetesAPI(t, mux)}
	if err := r.provision(context.Background(), "7", kubernetesClusterModel{}); err == nil || err.Error() != "the cluster is failed" {
		t.Fatalf("got %v", err)
	}
}

func TestDeleteClusterRefusesAProtectedCluster(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, true))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { log.add(r) })

	err := deleteCluster(context.Background(), testKubernetesAPI(t, mux), "7", false)
	if err == nil || !strings.Contains(err.Error(), "protected") || log.String() != "" {
		t.Fatalf("err %v, writes %q", err, log.String())
	}
}

func TestDeleteClusterUnprotectsAndWaitsUntilGone(t *testing.T) {
	fastKubernetesPoll(t)
	var log requestLog
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		switch gets.Add(1) {
		case 1:
			k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, true))
		case 2:
			k8sReply(w, http.StatusOK, clusterJSON("terminating", "1.35.7", true, false))
		default:
			k8sReply(w, http.StatusNotFound, `{"detail": "Not found."}`)
		}
	})
	mux.HandleFunc("PATCH "+clusterPath, func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		k8sReply(w, http.StatusOK, clusterJSON("active", "1.35.7", true, false))
	})
	mux.HandleFunc("DELETE "+clusterPath, func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		w.WriteHeader(http.StatusNoContent)
	})

	err := deleteCluster(context.Background(), testKubernetesAPI(t, mux), "7", true)
	want := "PATCH " + clusterPath + ` {"protected":false}` + "\nDELETE " + clusterPath
	if err != nil || log.String() != want || gets.Load() != 3 {
		t.Fatalf("err %v, writes %q, polls %d", err, log.String(), gets.Load())
	}
}

func TestDeleteClusterReportsAStuckDeletion(t *testing.T) {
	fastKubernetesPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		k8sReply(w, http.StatusOK, clusterJSON("failed", "1.35.7", false, false))
	})
	mux.HandleFunc("DELETE "+clusterPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	err := deleteCluster(context.Background(), testKubernetesAPI(t, mux), "7", false)
	if err == nil || err.Error() != "the cluster was not deleted; it is failed" {
		t.Fatalf("got %v", err)
	}
}

func TestFirstPoolChangesStopThePlanInsteadOfReplacing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		state, plan types.Int64
		wantError   bool
	}{
		{"resize after create", types.Int64Value(1), types.Int64Value(3), true},
		{"unchanged", types.Int64Value(1), types.Int64Value(1), false},
		{"filled in after import", types.Int64Null(), types.Int64Value(3), false},
		{"removed from config", types.Int64Value(1), types.Int64Null(), false},
	} {
		resp := planmodifier.Int64Response{PlanValue: tc.plan}
		firstPoolInt64{}.PlanModifyInt64(ctx, planmodifier.Int64Request{StateValue: tc.state, PlanValue: tc.plan}, &resp)
		if resp.Diagnostics.HasError() != tc.wantError || resp.RequiresReplace {
			t.Fatalf("%s: error %v replace %v", tc.name, resp.Diagnostics, resp.RequiresReplace)
		}
	}
	resp := planmodifier.StringResponse{}
	firstPoolString{}.PlanModifyString(ctx, planmodifier.StringRequest{
		StateValue: types.StringValue("k8s-2"), PlanValue: types.StringValue("k8s-4"),
	}, &resp)
	if !resp.Diagnostics.HasError() || resp.RequiresReplace {
		t.Fatalf("package change: %v", resp.Diagnostics)
	}
}
