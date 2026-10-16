package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func TestAccBucket(t *testing.T) {
	name := testAccName("bucket")
	const res = "pidginhost_bucket.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig(name, 1, false),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("full_name"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("endpoint"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testAccBucketConfig(name, 2, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("quota_gb"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("public_read"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status"), knownvalue.StringExact("active")),
				},
			},
		},
	})
}

func testAccBucketConfig(name string, quota int, public bool) string {
	return fmt.Sprintf(`
resource "pidginhost_bucket" "test" {
  name        = %q
  quota_gb    = %d
  public_read = %t
}
`, name, quota, public)
}

func TestWaitBucketActiveFollowsTheStatus(t *testing.T) {
	client, calls := fakeSequence(t, bucketJSON("provisioning"), bucketJSON("active"))
	bucket, err := waitBucketActive(testCtx(t), client, &pidginhost.Bucket{Id: 5, Status: "pending"})
	if err != nil || bucket.Status != "active" || calls.Load() != 2 {
		t.Fatalf("got %v, %v after %d calls", bucket, err, calls.Load())
	}
}

func TestWaitBucketActiveSkipsAnActiveBucket(t *testing.T) {
	client, calls := fakeSequence(t, bucketJSON("failed"))
	if _, err := waitBucketActive(testCtx(t), client, &pidginhost.Bucket{Id: 5, Status: "active"}); err != nil || calls.Load() != 0 {
		t.Fatalf("got %v after %d calls", err, calls.Load())
	}
}

func TestWaitBucketActiveFailsOnAFailedBucket(t *testing.T) {
	client, _ := fakeSequence(t, bucketJSON("provisioning"), bucketJSON("failed"))
	_, err := waitBucketActive(testCtx(t), client, &pidginhost.Bucket{Id: 5, Status: "pending"})
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitBucketGone(t *testing.T) {
	other := `{"id":6,"name":"other","full_name":"c1-other","quota_gb":1,"used_bytes":0,"object_count":0,` +
		`"public_read":false,"status":"active","endpoint":"","region":"","created":""}`
	cases := map[string]struct {
		lists   []string
		calls   int32
		failure string
	}{
		"left the list":       {lists: []string{"[" + bucketJSON("cancelling") + "," + other + "]", "[" + other + "]"}, calls: 2},
		"reached terminated":  {lists: []string{"[" + bucketJSON("terminating") + "]", "[" + bucketJSON("terminated") + "]"}, calls: 2},
		"deletion was undone": {lists: []string{"[" + bucketJSON("cancelling") + "]", "[" + bucketJSON("active") + "]"}, failure: "is active instead of deleted"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, calls := fakeSequence(t, tc.lists...)
			err := waitBucketGone(testCtx(t), client, 5)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("got %v, want %q", err, tc.failure)
				}
				return
			}
			if err != nil || calls.Load() != tc.calls {
				t.Fatalf("got %v after %d calls", err, calls.Load())
			}
		})
	}
}

func bucketJSON(status string) string {
	return fmt.Sprintf(`{"id":5,"name":"media","full_name":"c1-media","quota_gb":1,"used_bytes":0,"object_count":0,`+
		`"public_read":false,"status":%q,"endpoint":"https://s3.example.com","region":"default","created":""}`, status)
}

// fakeSequence serves bodies in order, one per request, repeating the last
// one, and shortens the storage polls for the test.
func fakeSequence(t *testing.T, bodies ...string) (*pidginhost.APIClient, *atomic.Int32) {
	t.Helper()
	fastStoragePolls(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[min(n, len(bodies))-1]))
	}))
	t.Cleanup(srv.Close)
	return newAPIClient("t", srv.URL, "test"), &calls
}

// testCtx bounds a wait under test, so broken logic fails instead of hanging.
func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func fastStoragePolls(t *testing.T) {
	saved := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = saved })
}
