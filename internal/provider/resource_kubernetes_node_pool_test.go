package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	pidginhost "github.com/pidginhost/sdk-go"
)

func journal(id int32, kind string, requested *int32, status string, hosts ...string) pidginhost.PoolRemovalJournal {
	items := make([]pidginhost.PoolRemovalItem, len(hosts))
	for i, h := range hosts {
		items[i] = pidginhost.PoolRemovalItem{TargetHostname: h}
	}
	j := pidginhost.PoolRemovalJournal{Id: id, Kind: pidginhost.PoolRemovalJournalKindEnum(kind),
		Status: pidginhost.PoolRemovalJournalStatusEnum(status), Items: items}
	j.RequestedPoolSize.Set(requested)
	return j
}

func TestRemovalJournalPicksThisChange(t *testing.T) {
	pool := &pidginhost.ResourcePool{Id: 3, Nodes: []pidginhost.ResourcePoolNode{{Name: "w1"}, {Name: "w2"}, {Name: "w3"}}}
	two, one := int32(2), int32(1)
	journals := []pidginhost.PoolRemovalJournal{
		journal(4, "downsize", &two, "succeeded", "w3"),     // before this change
		journal(9, "downsize", &two, "running", "other-w1"), // another pool
		journal(8, "pool_delete", nil, "running", "w1", "w2", "w3"),
		journal(7, "downsize", &one, "running", "w2", "w3"), // another size
		journal(6, "downsize", &two, "running", "w3"),       // this one
		journal(10, "downsize", &two, "running", "w3"),      // a later identical request
	}
	got := removalJournal(journals, 5, pool, &two)
	if got == nil || got.Id != 6 {
		t.Fatalf("downsize to 2: got %+v", got)
	}
	if got := removalJournal(journals, 5, pool, nil); got == nil || got.Id != 8 {
		t.Fatalf("pool delete: got %+v", got)
	}
	if got := removalJournal(journals, 10, pool, &two); got != nil {
		t.Fatalf("nothing after 10: got %+v", got)
	}
}

// fakeNodePool serves cluster 7 with pool 3 of three workers. A downsize
// opens a journal that finishes on the second read and drops the workers.
type fakeNodePool struct {
	mu       sync.Mutex
	size     int
	journals []string
	reads    int
	log      requestLog
}

func (f *fakeNodePool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet {
		f.log.add(r)
	}
	nodes := make([]string, f.size)
	for i := range nodes {
		nodes[i] = fmt.Sprintf(`{"id": %d, "name": "w%d", "ip": null}`, i+1, i+1)
	}
	pool := fmt.Sprintf(`{"id": 3, "package": "k8s-2", "generation": "g1", "size": %d, "nodes": [%s]}`, f.size, strings.Join(nodes, ","))
	switch {
	case r.URL.Path == clusterPath:
		k8sReply(w, 200, clusterJSON("active", "1.36.3", true, false))
	case r.URL.Path == clusterPath+"resource-pools/3/" && r.Method == http.MethodGet:
		k8sReply(w, 200, pool)
	case r.URL.Path == clusterPath+"resource-pools/3/" && r.Method == http.MethodPatch:
		f.journals = append(f.journals, `{"id": 12, "kind": "downsize", "status": "destructive", "reason": "", "message": "",
			"requested_pool_size": 2, "local_data_loss_accepted": true, "actor_label": "", "created_at": "", "updated_at": "",
			"finished_at": null, "items": [{"target_hostname": "w3", "cordoned_at": null, "drained_at": null,
			"detached_at": null, "validated_at": null, "reset_started_at": null, "reset_completed_at": null,
			"node_deleted_at": null, "vm_deleted_at": null, "uncordoned_at": null}], "allowed_actions": []}`)
		k8sReply(w, 200, pool)
	case r.URL.Path == clusterPath+"pool-removal-journals/":
		if len(f.journals) > 0 {
			if f.reads++; f.reads == 2 {
				f.journals[0] = strings.Replace(f.journals[0], `"status": "destructive"`, `"status": "succeeded"`, 1)
				f.size = 2
			}
		}
		k8sReply(w, 200, fmt.Sprintf(`{"count": %d, "next": null, "results": [%s]}`, len(f.journals), strings.Join(f.journals, ",")))
	default:
		k8sReply(w, 500, `{"detail": "unexpected `+r.Method+" "+r.URL.Path+`"}`)
	}
}

func TestNodePoolShrinkFollowsTheRemovalJournal(t *testing.T) {
	fastKubernetesPoll(t)
	fake := &fakeNodePool{size: 3}
	r := &kubernetesNodePoolResource{client: testKubernetesAPI(t, fake)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := r.resize(ctx, 7, "3", 2, true)
	if err != nil {
		t.Fatalf("resize: %v\nwrites:\n%s", err, &fake.log)
	}
	if pool.Size != 2 {
		t.Fatalf("pool size %d", pool.Size)
	}
	if got := fake.log.String(); got != `PATCH /api/kubernetes/clusters/7/resource-pools/3/ {"local_data_loss_accepted":true,"new_size":2}` {
		t.Fatalf("writes:\n%s", got)
	}
}

func TestNodePoolStateComesFromTheAPI(t *testing.T) {
	fake := &fakeNodePool{size: 2}
	r := &kubernetesNodePoolResource{client: testKubernetesAPI(t, fake)}
	s := testSchema(r)
	state := kubernetesNodePoolModel{
		ID: types.StringValue("7/3"), ClusterID: types.StringValue("7"), PoolID: types.StringValue("3"),
		Package: types.StringNull(), Generation: types.StringNull(), Size: types.Int64Null(),
		LocalDataLossAccepted: types.BoolNull(), Nodes: types.ListNull(types.ObjectType{AttrTypes: nodeAttrTypes}),
		Timeouts: nullTimeouts(t, s),
	}
	resp := resource.ReadResponse{State: testState(t, s, state)}
	r.Read(context.Background(), resource.ReadRequest{State: testState(t, s, state)}, &resp)
	var got kubernetesNodePoolModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if resp.Diagnostics.HasError() || got.Package.ValueString() != "k8s-2" || got.Size.ValueInt64() != 2 || len(got.Nodes.Elements()) != 2 {
		t.Fatalf("diags %v state %+v", resp.Diagnostics, got)
	}
}

func TestAccKubernetesNodePool(t *testing.T) {
	cluster := os.Getenv("PIDGINHOST_ACC_KUBERNETES_CLUSTER_ID")
	pkg := os.Getenv("PIDGINHOST_ACC_KUBERNETES_PACKAGE")
	config := func(size int) string {
		return fmt.Sprintf(`
resource "pidginhost_kubernetes_node_pool" "test" {
  cluster_id               = %q
  package                  = %q
  size                     = %d
  local_data_loss_accepted = true
}
`, cluster, pkg, size)
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			if cluster == "" || pkg == "" {
				t.Skip("needs PIDGINHOST_ACC_KUBERNETES_CLUSTER_ID and PIDGINHOST_ACC_KUBERNETES_PACKAGE")
			}
		},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []tfresource.TestStep{
			{Config: config(1)},
			{ResourceName: "pidginhost_kubernetes_node_pool.test", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"local_data_loss_accepted", "timeouts"}},
			{Config: config(2)},
			{Config: config(1)},
		},
	})
}
