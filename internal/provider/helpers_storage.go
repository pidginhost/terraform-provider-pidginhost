package provider

import (
	"fmt"
	"strconv"
	"time"
)

// storagePoll paces the waits of the storage resources; tests shorten it.
var storagePoll = 5 * time.Second

// apiID parses an object id kept as a decimal string in the state.
func apiID(id string) (int32, error) {
	n, err := strconv.ParseInt(id, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q: expected a number", id)
	}
	return int32(n), nil
}
