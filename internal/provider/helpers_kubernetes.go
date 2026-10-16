package provider

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// clusterIDAttribute is the cluster_id of the objects that belong to a
// Kubernetes cluster.
func clusterIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Required:      true,
		Description:   "ID of the Kubernetes cluster. Changing it replaces the object.",
		Validators:    []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]+$`), "must be a numeric ID")},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}
