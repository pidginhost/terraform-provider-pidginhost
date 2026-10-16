package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
)

// computePollInterval is how often the compute resources poll an object
// that the API changes asynchronously. Tests shorten it.
var computePollInterval = 5 * time.Second

// computeDefaultTimeout bounds the waits of the compute resources.
const computeDefaultTimeout = 20 * time.Minute

// numericID refuses ids that cannot name an API object, before the plan.
var numericID = stringvalidator.RegexMatches(regexp.MustCompile(`^[1-9][0-9]*$`), "must be a numeric id")

// parseInt32ID reads an API id kept as a decimal string.
func parseInt32ID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a numeric PidginHost id", s)
	}
	return int32(n), nil
}
