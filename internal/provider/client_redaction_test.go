package provider

import (
	"errors"
	"testing"
)

func TestRedactedAPIError(t *testing.T) {
	const reason = "Unknown package. "
	for _, tc := range []struct {
		name    string
		secrets []string
		echo    string
		want    string
	}{
		{
			name: "no submitted secrets", secrets: []string{""},
			echo: "Try another package.", want: "Try another package.",
		},
		{
			name: "whole password", secrets: []string{"test-password"},
			echo: "refused test-password", want: "refused (redacted)",
		},
		{
			name:    "escaped normalized script line",
			secrets: []string{"#!/bin/sh\r\ntoken=\"test-marker\"\r\n"},
			echo:    `refused "#!/bin/sh\ntoken=\"test-marker\""`,
			want:    `refused "(redacted)\n(redacted)"`,
		},
		{
			name: "password inside script", secrets: []string{"test-shared", "#!/bin/sh\ntoken=test-shared-extra\n"},
			echo: "refused token=test-shared-extra", want: "refused (redacted)",
		},
		{
			name: "overlapping lines", secrets: []string{"test-prefix-abcdefgh\nabcdefgh-suffix"},
			echo: "refused test-prefix-abcdefgh-suffix", want: "refused (redacted)",
		},
		{
			name: "overlapping repetitions", secrets: []string{"test-test"},
			echo: "refused test-test-test", want: "refused (redacted)",
		},
		{
			name: "trailing quote", secrets: []string{`test-marker"`},
			echo: `refused "test-marker\""`, want: `refused "(redacted)"`,
		},
		{
			name: "replacement text is not input", secrets: []string{"test-marker", "redacted"},
			echo: "refused test-marker", want: "refused (redacted)",
		},
		{
			name: "short script lines leave reason intact", secrets: []string{"#!/bin/sh\nfi\n"},
			echo: "Invalid configuration.", want: "Invalid configuration.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secrets := []*string{nil}
			for _, secret := range tc.secrets {
				secrets = append(secrets, &secret)
			}
			got := redactedAPIError("Creating server", errors.New(reason+tc.echo), secrets...)
			if got.Summary() != "Creating server" || got.Detail() != reason+tc.want {
				t.Fatal("error did not preserve the reason and redact all submitted text")
			}
		})
	}
}
