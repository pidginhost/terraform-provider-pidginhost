package provider

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// kubernetesPoll is how often the Kubernetes resources poll the API while
// they wait. Tests shorten it.
var kubernetesPoll = 5 * time.Second

// int32ID parses an API id kept in state as a decimal string.
func int32ID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid ID", s)
	}
	return int32(n), nil
}

// splitImportID splits a composite import ID such as "7/tcp/12" into its n
// parts. It reports false when the ID has another shape.
func splitImportID(id string, n int) ([]string, bool) {
	parts := strings.Split(id, "/")
	return parts, len(parts) == n && !slices.Contains(parts, "")
}

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
