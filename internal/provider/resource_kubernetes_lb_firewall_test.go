package provider

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccKubernetesLBFirewall(t *testing.T) {
	clusterID := testAccKubernetesClusterID(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccKubernetesLBFirewallConfig(clusterID, "tf-acc"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_lb_firewall.test", tfjsonpath.New("rule"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue("pidginhost_kubernetes_lb_firewall.test", tfjsonpath.New("rule").AtSliceIndex(0).AtMapKey("position"), knownvalue.Int64Exact(100)),
				},
			},
			{
				ResourceName:      "pidginhost_kubernetes_lb_firewall.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     clusterID,
			},
			{
				Config: testAccKubernetesLBFirewallConfig(clusterID, "tf-acc-renamed"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("pidginhost_kubernetes_lb_firewall.test", tfjsonpath.New("rule").AtSliceIndex(0).AtMapKey("comment"), knownvalue.StringExact("tf-acc-renamed")),
				},
			},
		},
	})
}

func testAccKubernetesLBFirewallConfig(clusterID, comment string) string {
	return fmt.Sprintf(`
resource "pidginhost_kubernetes_lb_firewall" "test" {
  cluster_id = %q

  rule = [
    {
      direction = "in"
      action    = "ACCEPT"
      protocol  = "tcp"
      source    = "192.0.2.0/24"
      dport     = "8443"
      comment   = %q
    },
    {
      direction = "in"
      action    = "DROP"
      protocol  = "tcp"
      dport     = "8443"
      position  = 200
    },
  ]
}
`, clusterID, comment)
}

func lbWebRule(id int32) lbRule {
	return lbRule{ID: id, Direction: "in", Action: "ACCEPT", Protocol: "tcp", Dport: "443", Enabled: true, Position: 100}
}

func TestDiffLBRules(t *testing.T) {
	ssh := lbRule{ID: 2, Direction: "in", Action: "ACCEPT", Protocol: "tcp", Dport: "22", Enabled: true, Position: 100}
	drop := lbRule{ID: 3, Direction: "in", Action: "DROP", Enabled: true, Position: 900}

	unchanged, renamed := lbWebRule(0), lbWebRule(0)
	renamed.Comment = "web"
	disabled := ssh
	disabled.ID, disabled.Enabled = 0, false
	icmp := lbRule{Direction: "in", Action: "ACCEPT", Protocol: "icmp", Enabled: true, Position: 100}

	create, patch, remove := diffLBRules([]lbRule{lbWebRule(1), ssh, drop}, []lbRule{renamed, disabled, icmp})
	renamed.ID, disabled.ID = 1, 2
	if !reflect.DeepEqual(create, []lbRule{icmp}) || !reflect.DeepEqual(patch, []lbRule{renamed, disabled}) ||
		!reflect.DeepEqual(remove, []lbRule{drop}) {
		t.Fatalf("create %v\npatch %v\nremove %v", create, patch, remove)
	}

	create, patch, remove = diffLBRules([]lbRule{lbWebRule(1)}, []lbRule{unchanged})
	if create != nil || patch != nil || len(remove) != 0 {
		t.Fatalf("a matching rule changed: %v %v %v", create, patch, remove)
	}
}

func TestDiffLBRulesMatchesDuplicatesOneToOne(t *testing.T) {
	create, patch, remove := diffLBRules([]lbRule{lbWebRule(1)}, []lbRule{lbWebRule(0), lbWebRule(0)})
	if len(create) != 1 || patch != nil || len(remove) != 0 {
		t.Fatalf("create %v patch %v remove %v", create, patch, remove)
	}
	create, patch, remove = diffLBRules([]lbRule{lbWebRule(1), lbWebRule(2)}, []lbRule{lbWebRule(0)})
	if create != nil || patch != nil || len(remove) != 1 || remove[0].ID != 2 {
		t.Fatalf("create %v patch %v remove %v", create, patch, remove)
	}
}

func TestOrderLBRulesFollowsThePriorOrder(t *testing.T) {
	ssh := lbRule{ID: 2, Direction: "in", Action: "ACCEPT", Protocol: "tcp", Dport: "22"}
	extra := lbRule{ID: 3, Direction: "out", Action: "DROP"}
	got := orderLBRules([]lbRule{lbWebRule(1), extra, ssh}, []lbRule{ssh, lbWebRule(0), {Direction: "in", Action: "REJECT"}})
	if ids := []int32{got[0].ID, got[1].ID, got[2].ID}; !reflect.DeepEqual(ids, []int32{2, 1, 3}) {
		t.Fatalf("got order %v", ids)
	}
}

func lbRuleJSON(id int, action, dport, comment string, position int) string {
	return fmt.Sprintf(`{"id": %d, "direction": "in", "action": %q, "protocol": "tcp", "source": null, "sport": null,
		"destination": null, "dport": %q, "comment": %q, "enabled": true, "position": %d,
		"created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z"}`, id, action, dport, comment, position)
}

func TestLBFirewallApplyDeletesPatchesThenCreates(t *testing.T) {
	var log requestLog
	var mu sync.Mutex
	rules := []string{lbRuleJSON(1, "ACCEPT", "443", "", 100), lbRuleJSON(2, "ACCEPT", "22", "", 100)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+clusterPath+"lb-firewall/", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		k8sReply(w, http.StatusOK, fmt.Sprintf(`{"count": %d, "next": null, "previous": null, "results": [%s]}`,
			len(rules), strings.Join(rules, ",")))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodDelete:
			rules = rules[:1]
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			rules[0] = lbRuleJSON(1, "ACCEPT", "443", "web", 100)
			k8sReply(w, http.StatusOK, rules[0])
		case http.MethodPost:
			rules = append(rules, lbRuleJSON(3, "DROP", "8080", "", 100))
			k8sReply(w, http.StatusCreated, rules[1])
		}
	})

	m := kubernetesLBFirewallModel{ClusterID: types.StringValue("7")}
	m.setRules([]lbRule{
		{Direction: "in", Action: "DROP", Protocol: "tcp", Dport: "8080", Enabled: true, Position: 100},
		{Direction: "in", Action: "ACCEPT", Protocol: "tcp", Dport: "443", Comment: "web", Enabled: true, Position: 100},
	})
	r := &kubernetesLBFirewallResource{client: testKubernetesAPI(t, mux)}
	if err := r.apply(context.Background(), &m); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"DELETE " + clusterPath + "lb-firewall/2/",
		"PATCH " + clusterPath + `lb-firewall/1/ {"comment":"web","enabled":true,"position":100}`,
		"POST " + clusterPath + `lb-firewall/ {"action":"DROP","direction":"in","dport":"8080","enabled":true,"position":100,"protocol":"tcp"}`,
	}, "\n")
	if log.String() != want {
		t.Fatalf("writes:\n%s\nwant:\n%s", log.String(), want)
	}
	if m.ID.ValueString() != "7" || len(m.Rules) != 2 || m.Rules[0].Dport.ValueString() != "8080" || m.Rules[1].Comment.ValueString() != "web" {
		t.Fatalf("state %+v", m)
	}
}
