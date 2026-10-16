package provider

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	pidginhost "github.com/pidginhost/sdk-go"
)

const (
	testRefused = `Snapshot "s1" could not be created: the server is not active.`
	testBusy    = `Snapshot "s1" could not be created: another server operation is in progress.`
)

var testRefusals = []string{`Snapshot "s1" could not be created`, `Snapshot "s1" was not created`}

func logEntry(date, message string) pidginhost.ActivityLogEntry {
	return pidginhost.ActivityLogEntry{Date: date, Message: message}
}

func TestActivityRefusal(t *testing.T) {
	old := logEntry("10:00", testRefused)
	before := activityCounts([]pidginhost.ActivityLogEntry{old})
	cases := map[string]struct {
		logs  []pidginhost.ActivityLogEntry
		final string
		busy  bool
	}{
		"entries from before are ignored": {logs: []pidginhost.ActivityLogEntry{old, logEntry("10:01", "Server rebooted.")}},
		"a repeat of an old entry is new": {logs: []pidginhost.ActivityLogEntry{old, old}, final: testRefused},
		"busy is not final":               {logs: []pidginhost.ActivityLogEntry{logEntry("10:02", testBusy), old}, busy: true},
		"a final refusal ends the scan": {
			logs:  []pidginhost.ActivityLogEntry{logEntry("10:02", testBusy), logEntry("10:03", `Snapshot "s1" was not created.`)},
			final: `Snapshot "s1" was not created.`, busy: true,
		},
		"other snapshots are ignored": {logs: []pidginhost.ActivityLogEntry{logEntry("10:02", `Snapshot "s10" could not be created.`)}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			final, busy := activityRefusal(before, tc.logs, testRefusals)
			if final != tc.final || busy != tc.busy {
				t.Fatalf("got %q, %v; want %q, %v", final, busy, tc.final, tc.busy)
			}
		})
	}
}

func TestWaitActivityResendsWhenTheServerWasBusy(t *testing.T) {
	fastStoragePolls(t)
	busy := []pidginhost.ActivityLogEntry{logEntry("10:01", testBusy)}
	polls, submits := 0, 0
	logs := func() ([]pidginhost.ActivityLogEntry, error) { polls++; return busy, nil }
	submit := func() error { submits++; return nil }
	// The resend moves the busy refusal into the baseline, so the second poll asks done.
	done := func() (bool, error) { return polls == 2, nil }

	if err := waitActivity(testCtx(t), logs, activityCounts(nil), testRefusals, submit, done); err != nil {
		t.Fatal(err)
	}
	if polls != 2 || submits != 1 {
		t.Fatalf("%d polls, %d resends", polls, submits)
	}
}

func TestWaitActivitySpacesResends(t *testing.T) {
	fastStoragePolls(t)
	polls, submits := 0, 0
	// Every poll finds a new busy refusal.
	logs := func() ([]pidginhost.ActivityLogEntry, error) {
		polls++
		return []pidginhost.ActivityLogEntry{logEntry(strconv.Itoa(polls), testBusy)}, nil
	}
	submit := func() error { submits++; return nil }
	done := func() (bool, error) { t.Error("done asked while the server was busy"); return false, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := waitActivity(ctx, logs, activityCounts(nil), testRefusals, submit, done)
	if !errors.Is(err, context.DeadlineExceeded) || polls < 3 || submits != 1 {
		t.Fatalf("got %v after %d polls, %d resends", err, polls, submits)
	}
}

func TestWaitActivityFailsOnARefusal(t *testing.T) {
	fastStoragePolls(t)
	logs := func() ([]pidginhost.ActivityLogEntry, error) {
		return []pidginhost.ActivityLogEntry{logEntry("10:01", testRefused)}, nil
	}
	submit := func() error { t.Error("resent a refused operation"); return nil }
	done := func() (bool, error) { t.Error("done asked after a refusal"); return true, nil }

	err := waitActivity(testCtx(t), logs, activityCounts(nil), testRefusals, submit, done)
	if err == nil || err.Error() != testRefused {
		t.Fatalf("got %v", err)
	}
}
