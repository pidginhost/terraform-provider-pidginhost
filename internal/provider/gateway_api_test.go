package provider

import (
	"context"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// planGatewayAPI runs the enable_gateway_api plan modifiers the way the
// framework does for an update: a computed attribute missing from the
// configuration starts out unknown.
func planGatewayAPI(t *testing.T, state, config types.Bool) planmodifier.BoolResponse {
	t.Helper()
	var s fwresource.SchemaResponse
	(&kubernetesClusterResource{}).Schema(context.Background(), fwresource.SchemaRequest{}, &s)
	attr := s.Schema.Attributes["enable_gateway_api"].(schema.BoolAttribute)
	raw := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	plan := config
	if config.IsNull() && attr.Computed {
		plan = types.BoolUnknown()
	}
	req := planmodifier.BoolRequest{
		State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw},
		StateValue: state, ConfigValue: config, PlanValue: plan,
	}
	resp := planmodifier.BoolResponse{PlanValue: plan}
	for _, m := range attr.PlanModifiers {
		req.PlanValue = resp.PlanValue
		m.PlanModifyBool(context.Background(), req, &resp)
	}
	return resp
}

func TestRemovingGatewayAPIFromConfigKeepsTheCluster(t *testing.T) {
	resp := planGatewayAPI(t, types.BoolValue(true), types.BoolNull())
	if resp.RequiresReplace {
		t.Fatal("removing enable_gateway_api from the configuration replaces the cluster")
	}
}

func TestTurningGatewayAPIOffReplacesTheCluster(t *testing.T) {
	// A cluster created without the setting has it on: the API enables it by default.
	resp := planGatewayAPI(t, types.BoolValue(true), types.BoolValue(false))
	if !resp.RequiresReplace {
		t.Fatal("turning the Gateway API off must replace the cluster, the API cannot change it")
	}
}
