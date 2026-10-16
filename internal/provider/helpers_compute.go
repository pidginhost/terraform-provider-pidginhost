package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// computeDefaultTimeout bounds the waits of the compute resources.
const computeDefaultTimeout = 20 * time.Minute

// numericID refuses ids that cannot name an API object, before the plan.
var numericID = stringvalidator.RegexMatches(regexp.MustCompile(`^[1-9][0-9]*$`), "must be a numeric id")

// splitImportID splits an import id of the form "a/b" into n non-empty parts.
func splitImportID(id string, n int) ([]string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != n || slices.Contains(parts, "") {
		return nil, false
	}
	return parts, true
}

// importIPVersionAndID imports an address resource by "<version>/<id>": IPv4
// and IPv6 addresses have separate ids, so the id alone is ambiguous.
func importIPVersionAndID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, 2)
	if !ok || (parts[0] != "4" && parts[0] != "6") {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("Expected <version>/<id>, such as 4/1234; got %q.", req.ID))
		return
	}
	if _, err := parseID(parts[1]); err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	version, _ := strconv.ParseInt(parts[0], 10, 64)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), version)...)
}
