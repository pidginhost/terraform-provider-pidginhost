package provider

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	pidginhost "github.com/pidginhost/sdk-go"
)

// activityResend spaces the resubmissions of a server operation refused
// because another operation held the server.
var activityResend = 30 * time.Second

// serverBusy is how the API words a refusal that ran nothing because another
// operation held the server.
const serverBusy = "another server operation is in progress"

// serverActivity returns a reader of a server's activity log.
func serverActivity(ctx context.Context, client *pidginhost.APIClient, serverID int32) func() ([]pidginhost.ActivityLogEntry, error) {
	return func() ([]pidginhost.ActivityLogEntry, error) {
		activity, _, err := client.CloudAPI.CloudServersActivityRetrieve(ctx, serverID).Execute()
		if err != nil {
			return nil, err
		}
		return activity.Logs, nil
	}
}

type activityKey struct{ date, message string }

// activityCounts counts the entries of an activity log. The log has no ids,
// so entries logged later are told apart from older ones by these counts.
func activityCounts(logs []pidginhost.ActivityLogEntry) map[activityKey]int {
	counts := map[activityKey]int{}
	for _, entry := range logs {
		counts[activityKey{entry.Date, entry.Message}]++
	}
	return counts
}

// activityRefusal looks at the entries logged since before for messages that
// start with one of refusals. It returns the first refusal that is final, and
// whether a refusal said only that the server was busy.
func activityRefusal(before map[activityKey]int, logs []pidginhost.ActivityLogEntry, refusals []string) (final string, busy bool) {
	old := maps.Clone(before)
	for _, entry := range logs {
		key := activityKey{entry.Date, entry.Message}
		if old[key] > 0 {
			old[key]--
			continue
		}
		if !slices.ContainsFunc(refusals, func(prefix string) bool { return strings.HasPrefix(entry.Message, prefix) }) {
			continue
		}
		if !strings.Contains(entry.Message, serverBusy) {
			return entry.Message, busy
		}
		busy = true
	}
	return "", busy
}

// waitActivity follows a server operation the API accepted with 202 and runs
// later, so a refusal only shows in the server's activity log. before counts
// the log as it was before submit first ran. It fails on a new refusal; while
// the refusals only say the server was busy, it sends the request again
// through submit, at most every activityResend. Otherwise it returns once
// done reports the operation complete.
func waitActivity(ctx context.Context, logs func() ([]pidginhost.ActivityLogEntry, error), before map[activityKey]int,
	refusals []string, submit func() error, done func() (bool, error)) error {
	var resendAt time.Time
	return waitFor(ctx, pollInterval, func() (bool, error) {
		entries, err := logs()
		if err != nil {
			return false, err
		}
		final, busy := activityRefusal(before, entries, refusals)
		switch {
		case final != "":
			return false, errors.New(final)
		case !busy:
			return done()
		case time.Now().Before(resendAt):
			return false, nil
		}
		before = activityCounts(entries)
		resendAt = time.Now().Add(activityResend)
		return false, submit()
	})
}
