package provider

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

func nodePoolPlan(t *testing.T, r *kubernetesNodePoolResource) kubernetesNodePoolModel {
	t.Helper()
	return kubernetesNodePoolModel{
		ID: types.StringValue("7/3"), ClusterID: types.StringValue("7"), PoolID: types.StringValue("3"),
		Package: types.StringValue("k8s-2"), Generation: types.StringUnknown(), Size: types.Int64Value(1),
		LocalDataLossAccepted: types.BoolValue(true), Nodes: types.ListUnknown(types.ObjectType{AttrTypes: nodeAttrTypes}),
		Timeouts: nullTimeouts(t, testSchema(r)),
	}
}

func TestNodePoolCreateKeepsConsentOnFailure(t *testing.T) {
	created := false
	client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
		switch call {
		case "GET /api/kubernetes/clusters/7/":
			if created {
				return 400, map[string]any{"detail": "cluster provisioning failed"}
			}
			return 200, pidginhost.ClusterDetail{Status: "active"}
		case "POST /api/kubernetes/clusters/7/resource-pools/":
			created = true
			return 201, pidginhost.ResourcePoolAddResponse{Id: 3}
		}
		return 500, nil
	})
	r := &kubernetesNodePoolResource{client: client}
	s := testSchema(r)
	plan := nodePoolPlan(t, r)
	plan.Timeouts.Object = types.ObjectValueMust(plan.Timeouts.AttributeTypes(t.Context()), map[string]attr.Value{
		"create": types.StringNull(), "update": types.StringNull(), "delete": types.StringValue("2m"),
	})
	resp := resource.CreateResponse{State: testState(t, s, nil)}
	r.Create(t.Context(), resource.CreateRequest{Plan: testPlan(t, s, plan)}, &resp)
	if !resp.Diagnostics.HasError() || !created {
		t.Fatalf("expected failure after creation: %v", resp.Diagnostics)
	}
	var got kubernetesNodePoolModel
	if diags := resp.State.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got.ID.ValueString() != "7/3" || !got.LocalDataLossAccepted.ValueBool() || !got.Timeouts.Equal(plan.Timeouts) {
		t.Fatalf("cleanup state lost: %+v", got)
	}
}

func TestNodePoolWaitStopsWhenClusterDisappears(t *testing.T) {
	client, calls := fakeAPI(t, func(string, map[string]any) (int, any) { return 404, nil })
	r := &kubernetesNodePoolResource{client: client}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := r.waitCluster(ctx, 7)
	if err == nil || !strings.Contains(err.Error(), "gone") || len(calls()) != 1 {
		t.Fatalf("expected immediate missing-cluster error, got %v after %d calls", err, len(calls()))
	}
}

func TestNodePoolDeleteUsesSettledWorkers(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "needs_attention"} {
		t.Run(terminal, func(t *testing.T) {
			settled, deleted, journalReads := false, false, 0
			client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
				switch call {
				case "GET /api/kubernetes/clusters/7/":
					settled = true
					return 200, pidginhost.ClusterDetail{Status: "active"}
				case "GET /api/kubernetes/clusters/7/resource-pools/3/":
					pool := pidginhost.ResourcePool{Id: 3, Package: "k8s-2", Generation: "g1", Nodes: []pidginhost.ResourcePoolNode{}}
					if settled {
						pool.Size, pool.Nodes = 1, []pidginhost.ResourcePoolNode{{Id: 1, Name: "w1"}}
					}
					return 200, pool
				case "DELETE /api/kubernetes/clusters/7/resource-pools/3/":
					deleted = true
					return 204, nil
				case "GET /api/kubernetes/clusters/7/pool-removal-journals/?page=1":
					journals := []pidginhost.PoolRemovalJournal{}
					if deleted {
						journalReads++
						status := "destructive"
						if journalReads > 1 {
							status = terminal
						}
						journals = append(journals, journal(12, "pool_delete", nil, status, "w1"))
					}
					return 200, pidginhost.PaginatedPoolRemovalJournalList{Results: journals}
				}
				return 500, nil
			})
			r := &kubernetesNodePoolResource{client: client}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			resp := resource.DeleteResponse{}
			r.Delete(ctx, resource.DeleteRequest{State: testState(t, testSchema(r), nodePoolPlan(t, r))}, &resp)
			if journalReads < 2 || resp.Diagnostics.HasError() != (terminal != "succeeded") {
				t.Fatalf("journal reads %d, diagnostics %v", journalReads, resp.Diagnostics)
			}
		})
	}
}

func TestSDKIntegerInputsRejectOverflow(t *testing.T) {
	for _, tc := range []struct {
		r         resource.Resource
		attribute string
	}{{&kubernetesNodePoolResource{}, "size"}, {&dnsRecordResource{}, "ttl"}} {
		t.Run(tc.attribute, func(t *testing.T) {
			a := testSchema(tc.r).Attributes[tc.attribute].(schema.Int64Attribute)
			for _, value := range []int64{1, math.MaxInt32, math.MaxInt32 + 1, 1<<32 + 1} {
				var resp validator.Int64Response
				for _, v := range a.Validators {
					v.ValidateInt64(t.Context(), validator.Int64Request{Path: path.Root(tc.attribute), ConfigValue: types.Int64Value(value)}, &resp)
				}
				if resp.Diagnostics.HasError() != (value > math.MaxInt32) {
					t.Errorf("%s=%d: %v", tc.attribute, value, resp.Diagnostics)
				}
			}
		})
	}
}

func TestNodePoolShrinkRejectsUnsuccessfulJournal(t *testing.T) {
	for _, terminal := range []string{"failed", "needs_attention"} {
		t.Run(terminal, func(t *testing.T) {
			patched := false
			client, _ := fakeAPI(t, func(call string, _ map[string]any) (int, any) {
				switch call {
				case "GET /api/kubernetes/clusters/7/":
					return 200, pidginhost.ClusterDetail{Status: "active"}
				case "GET /api/kubernetes/clusters/7/resource-pools/3/":
					return 200, pidginhost.ResourcePool{Id: 3, Size: 2, Nodes: []pidginhost.ResourcePoolNode{{Name: "w1"}, {Name: "w2"}}}
				case "PATCH /api/kubernetes/clusters/7/resource-pools/3/":
					patched = true
					return 200, pidginhost.ResourcePool{Id: 3}
				case "GET /api/kubernetes/clusters/7/pool-removal-journals/?page=1":
					journals := []pidginhost.PoolRemovalJournal{journal(10, "downsize", pidginhost.PtrInt32(1), "succeeded", "w2")}
					if patched {
						journals = append(journals, journal(11, "downsize", pidginhost.PtrInt32(1), terminal, "w2"),
							journal(12, "downsize", pidginhost.PtrInt32(1), "succeeded", "w2"))
					}
					return 200, pidginhost.PaginatedPoolRemovalJournalList{Results: journals}
				}
				return 500, nil
			})
			r := &kubernetesNodePoolResource{client: client}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := r.resize(ctx, 7, "3", 1, true); err == nil || !strings.Contains(fmt.Sprint(err), terminal) {
				t.Fatalf("expected %s from this change, got %v", terminal, err)
			}
		})
	}
}

func TestNodePoolChangesWaitForOtherPools(t *testing.T) {
	fastKubernetesPoll(t)
	started, release := make(chan struct{}), make(chan struct{})
	var writes atomic.Int32
	client := testKubernetesAPI(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == clusterPath:
			k8sReply(w, 200, clusterJSON("active", "1.36.3", true, false))
		case strings.HasSuffix(req.URL.Path, "pool-removal-journals/"):
			k8sReply(w, 200, `{"count":0,"next":null,"results":[]}`)
		case req.Method == http.MethodPatch:
			if writes.Add(1) > 1 {
				k8sReply(w, 409, `{"detail":"another pool change is running"}`)
				return
			}
			close(started)
			<-release
			k8sReply(w, 200, `{"id":3,"package":"k8s-2","generation":"g1","size":2,"nodes":[]}`)
		default:
			k8sReply(w, 200, `{"id":3,"package":"k8s-2","generation":"g1","size":2,"nodes":[]}`)
		}
	}))
	r := &kubernetesNodePoolResource{client: client}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.resize(ctx, 7, "3", 2, true)
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		close(release)
		t.Fatal("first pool change did not start")
	}
	secondCtx, secondCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, secondErr := r.resize(secondCtx, 7, "4", 2, true)
	secondCancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first resize: %v", err)
	}
	if secondErr == nil || writes.Load() != 1 {
		t.Fatalf("second change must wait for the first: error %v, writes %d", secondErr, writes.Load())
	}
}
