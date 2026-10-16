package provider

import (
	"context"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
)

// The acceptance tests do not run offline, so check the schemas here.
func TestKubernetesSchemasAreValid(t *testing.T) {
	ctx := context.Background()
	for _, newResource := range []func() fwresource.Resource{
		NewKubernetesClusterResource,
		NewKubernetesRouteResource,
		NewKubernetesPortForwardResource,
		NewKubernetesLBFirewallResource,
	} {
		var resp fwresource.SchemaResponse
		newResource().Schema(ctx, fwresource.SchemaRequest{}, &resp)
		resp.Diagnostics.Append(resp.Schema.ValidateImplementation(ctx)...)
		if resp.Diagnostics.HasError() {
			t.Errorf("%T: %v", newResource(), resp.Diagnostics)
		}
	}
}
