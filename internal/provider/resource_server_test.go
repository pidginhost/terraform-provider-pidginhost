package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestUpgradeServerV0KeepsServerAndNamesDroppedObjects(t *testing.T) {
	old := serverModelV0{
		ID:                  types.StringValue("42"),
		Image:               types.StringValue("ubuntu-24"),
		Package:             types.StringValue("cloudv-1"),
		Hostname:            types.StringValue("web1"),
		FwRulesSet:          types.StringValue("web"),
		FirewallRulesCreate: types.BoolValue(true),
		FirewallRulesSetID:  types.StringValue("7"),
		ExtraVolumeID:       types.StringValue("9"),
		ExtraVolumeAlias:    types.StringValue("web1-extra"),
		DestroyPublicIPv4:   types.BoolNull(),
		DestroyPublicIPv6:   types.BoolValue(true),
	}
	m, notes := upgradeServerV0(old)
	if m.ID.ValueString() != "42" || m.Package.ValueString() != "cloudv-1" || m.FwRulesSet.ValueString() != "web" {
		t.Fatalf("server attributes lost: %+v", m)
	}
	if m.DestroyPublicIPv4.ValueBool() || !m.DestroyPublicIPv6.ValueBool() {
		t.Fatalf("destroy flags: %v %v", m.DestroyPublicIPv4, m.DestroyPublicIPv6)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], `id = "9"`) || !strings.Contains(notes[1], `id = "7"`) {
		t.Fatalf("notes: %q", notes)
	}
	// Every import the notes advise must target a resource this provider has.
	registered := map[string]bool{}
	for _, factory := range New("test")().Resources(context.Background()) {
		var meta fwresource.MetadataResponse
		factory().Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: "pidginhost"}, &meta)
		registered[meta.TypeName] = true
	}
	for _, note := range notes {
		target := regexp.MustCompile(`import \{ to = (pidginhost_[a-z_]+)\.`).FindStringSubmatch(note)
		if target == nil || !registered[target[1]] {
			t.Fatalf("note does not advise importing into a registered resource: %q", note)
		}
	}

	old.FirewallRulesCreate = types.BoolValue(false) // a set the user brought, not one 0.1.x made
	old.ExtraVolumeID = types.StringNull()
	if _, notes := upgradeServerV0(old); len(notes) != 0 {
		t.Fatalf("unexpected notes: %q", notes)
	}
}

// fakeServerAPI is the part of the API pidginhost_server talks to.
type fakeServerAPI struct {
	mu        sync.Mutex
	detail    map[string]any
	reads     int // reads since the last state change
	upgradeTo string
	deleted   bool
	released  []string
	requests  []string
}

func newFakeServerAPI() *fakeServerAPI {
	return &fakeServerAPI{}
}

func (f *fakeServerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}

	switch path := r.URL.Path; {
	case r.Method == http.MethodPost && path == "/api/cloud/servers/":
		hostname, _ := body["hostname"].(string)
		if hostname == "" {
			hostname = "generated"
		}
		public := map[string]any{}
		if body["new_ipv4"] == true {
			public["ipv4"] = "192.0.2.10"
		}
		f.detail = map[string]any{
			"id": 101, "hostname": hostname, "image": body["image"], "package": body["package"],
			"cpus": 1, "memory": 1, "disk_size": 20, "generation": "gp", "machine": map[string]any{},
			"volumes": []any{}, "floating_ips": []any{}, "status": "provisioning", "username": "ubuntu",
			"destroy_protection": false, "ha_enabled": false, "custom_os": false, "rescue_mode": false,
			"boot_iso": nil, "rescue_supported": true, "project": "", "billing": nil,
			"networks": map[string]any{"public": public, "private": []any{}},
		}
		f.reads = 0
		reply(http.StatusCreated, map[string]any{"id": 101})
	case path == "/api/cloud/servers/101/" && r.Method == http.MethodGet:
		if f.detail == nil || f.deleted {
			reply(http.StatusNotFound, map[string]any{"detail": "Not found."})
			return
		}
		f.reads++
		// Pending servers answer 404 first, then report provisioning.
		if f.reads == 1 && f.detail["status"] == "provisioning" {
			reply(http.StatusNotFound, map[string]any{"detail": "Not found."})
			return
		}
		if f.reads >= 3 && (f.detail["status"] == "provisioning" || f.detail["status"] == "upgrading") {
			f.detail["status"] = "active"
			if f.upgradeTo != "" {
				f.detail["package"], f.upgradeTo = f.upgradeTo, ""
				f.detail["cpus"], f.detail["memory"], f.detail["disk_size"] = 2, 4, 40
			}
		}
		reply(http.StatusOK, f.detail)
	case path == "/api/cloud/servers/101/" && r.Method == http.MethodPatch:
		if p, ok := body["project"]; ok {
			f.detail["project"] = p
		}
		reply(http.StatusOK, f.detail)
	case path == "/api/cloud/servers/101/" && r.Method == http.MethodDelete:
		f.deleted = true
		reply(http.StatusNoContent, nil)
	case path == "/api/cloud/servers/101/modify-package/":
		f.detail["status"], f.upgradeTo, f.reads = "upgrading", body["package"].(string), 0
		reply(http.StatusOK, map[string]any{"upgrading": true})
	case path == "/api/cloud/servers/101/destroy-protection/":
		f.detail["destroy_protection"] = body["destroy_protection"]
		reply(http.StatusOK, map[string]any{"destroy_protection": body["destroy_protection"]})
	case path == "/api/cloud/servers/101/public-interface/":
		if len(f.detail["networks"].(map[string]any)["public"].(map[string]any)) == 0 {
			reply(http.StatusNotFound, map[string]any{"detail": "No public interface."})
			return
		}
		reply(http.StatusOK, map[string]any{
			"interface": "eth0", "ipv4": "192.0.2.10", "ipv6": "",
			"fw_rules_set": nil, "fw_policy_in": "DROP", "fw_policy_out": "ACCEPT",
		})
	case path == "/api/cloud/ipv4/":
		reply(http.StatusOK, map[string]any{"count": 2, "next": nil, "results": []any{
			map[string]any{"id": 7, "slug": "192.0.2.10", "address": "192.0.2.10", "gateway": "192.0.2.1",
				"prefix": 24, "attached": true, "server": "web1", "server_id": 101},
			map[string]any{"id": 8, "slug": "192.0.2.11", "address": "192.0.2.11", "gateway": "192.0.2.1",
				"prefix": 24, "attached": true, "server": "other", "server_id": 202},
		}})
	case strings.HasPrefix(path, "/api/cloud/ipv4/") && r.Method == http.MethodDelete:
		f.released = append(f.released, strings.Trim(strings.TrimPrefix(path, "/api/cloud/ipv4/"), "/"))
		reply(http.StatusNoContent, nil)
	default:
		reply(http.StatusNotImplemented, map[string]any{"detail": "fake API has no " + r.Method + " " + path})
	}
}

func TestServerLifecycleAgainstFakeAPI(t *testing.T) {
	fake := newFakeServerAPI()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	defer func(old time.Duration) { pollInterval = old }(pollInterval)
	pollInterval = 10 * time.Millisecond

	config := func(pkg string, protected bool) string {
		return fmt.Sprintf(`
provider "pidginhost" {
  token = "test"
  base_url = %q
}
resource "pidginhost_server" "test" {
  hostname            = "web1"
  image               = "ubuntu-24"
  package             = %q
  new_ipv4            = true
  fw_policy_in        = "DROP"
  destroy_protection  = %t
  destroy_public_ipv4 = true
}
`, srv.URL, pkg, protected)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("cloudv-1", false),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("status"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("ipv4_address"), knownvalue.StringExact("192.0.2.10")),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("fw_policy_in"), knownvalue.StringExact("DROP")),
				},
			},
			{
				Config: config("cloudv-2", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("pidginhost_server.test", plancheck.ResourceActionUpdate),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("package"), knownvalue.StringExact("cloudv-2")),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("cpus"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("memory"), knownvalue.Int64Exact(4)),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("disk_size"), knownvalue.Int64Exact(40)),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("destroy_protection"), knownvalue.Bool(true)),
				},
			},
			{
				ResourceName:            "pidginhost_server.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"new_ipv4", "destroy_public_ipv4", "destroy_public_ipv6", "timeouts"},
			},
			{
				// Protection has to come off before the destroy that ends the test.
				Config: config("cloudv-2", false),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if !fake.deleted {
				return fmt.Errorf("server was not deleted")
			}
			if strings.Join(fake.released, ",") != "7" {
				return fmt.Errorf("released %v, want only the server's own address 7", fake.released)
			}
			return nil
		},
	})
}

func TestServerOptionalComputedDefaults(t *testing.T) {
	for _, network := range []string{"new_ipv4 = true", "no_network_acknowledged = true"} {
		t.Run(network, func(t *testing.T) {
			fake := newFakeServerAPI()
			srv := httptest.NewServer(fake)
			defer srv.Close()
			old := pollInterval
			pollInterval = time.Millisecond
			t.Cleanup(func() { pollInterval = old })
			config := func(extra string) string {
				return fmt.Sprintf(`
provider "pidginhost" {
  token = "test"
  base_url = %q
}
resource "pidginhost_server" "test" {
  image = "ubuntu-24"
  package = "cloudv-1"
  %s
  %s
}
`, srv.URL, network, extra)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProviders,
				Steps: []resource.TestStep{
					{Config: config(""), ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("hostname"), knownvalue.StringExact("generated")),
					}},
					{Config: config(`project = "example"`), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("pidginhost_server.test", plancheck.ResourceActionUpdate),
					}}},
					{Config: config(""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("pidginhost_server.test", plancheck.ResourceActionNoop),
					}}},
					{Config: config(`project = ""`), ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("project"), knownvalue.StringExact("")),
					}},
				},
			})
		})
	}
}

func TestAccServer(t *testing.T) {
	key := testAccSSHPublicKey(t)
	testAccForgetAccountKey(t, key)
	hostname := testAccName("srv")
	image := os.Getenv("PIDGINHOST_ACC_IMAGE")
	if image == "" {
		image = "ubuntu24"
	}
	pkg := os.Getenv("PIDGINHOST_ACC_PACKAGE")
	if pkg == "" {
		pkg = "cloudv-1"
	}
	config := func(project string) string {
		return fmt.Sprintf(`
resource "pidginhost_server" "test" {
  hostname            = %q
  image               = %q
  package             = %q
  project             = %q
  ssh_pub_key         = %q
  new_ipv4            = true
  destroy_public_ipv4 = true
}
`, hostname, image, pkg, project, key)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("tf-acc"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("status"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("ipv4_address"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:            "pidginhost_server.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"new_ipv4", "destroy_public_ipv4", "destroy_public_ipv6", "timeouts", "ssh_pub_key"},
			},
			{
				Config: config("tf-acc-renamed"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_server.test", tfjsonpath.New("project"), knownvalue.StringExact("tf-acc-renamed")),
				},
			},
		},
	})
}
