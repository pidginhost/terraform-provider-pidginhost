package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestHasAuthentication(t *testing.T) {
	tests := []struct {
		name string
		plan serverResourceModel
		want bool
	}{
		{
			name: "password",
			plan: serverResourceModel{Password: types.StringValue("secret")},
			want: true,
		},
		{
			name: "ssh pub key",
			plan: serverResourceModel{SSHPubKey: types.StringValue("ssh-rsa AAA")},
			want: true,
		},
		{
			name: "ssh key id",
			plan: serverResourceModel{SSHPubKeyID: types.StringValue("key-123")},
			want: true,
		},
		{
			name: "none provided",
			plan: serverResourceModel{},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasAuthentication(&tc.plan)
			if got != tc.want {
				t.Fatalf("hasAuthentication() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringValue(t *testing.T) {
	cases := []struct {
		name  string
		value types.String
		want  string
	}{
		{name: "empty", value: types.StringNull(), want: ""},
		{name: "unknown", value: types.StringUnknown(), want: ""},
		{name: "value", value: types.StringValue("slug-123"), want: "slug-123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stringValue(tc.value); got != tc.want {
				t.Fatalf("stringValue() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandFirewallRule(t *testing.T) {
	t.Run("all fields populated", func(t *testing.T) {
		rule := firewallRuleModel{
			Direction:   types.StringValue("in"),
			Action:      types.StringValue("ACCEPT"),
			Protocol:    types.StringValue("tcp"),
			Source:      types.StringValue("0.0.0.0/0"),
			Sport:       types.StringValue("1024"),
			Destination: types.StringValue("10.0.0.1"),
			Dport:       types.StringValue("22"),
			Enabled:     types.BoolValue(true),
			Position:    types.StringValue("1"),
		}

		body, diags := expandFirewallRule(rule, 0)
		if diags.HasError() {
			t.Fatalf("expandFirewallRule() returned diagnostics: %v", diags)
		}

		if len(body) != 9 {
			t.Fatalf("expandFirewallRule() len = %d, want 9", len(body))
		}

		assertField := func(key, want string) {
			got, ok := body[key]
			if !ok {
				t.Fatalf("expected field %q to be present", key)
			}
			if got != want {
				t.Fatalf("field %q = %#v, want %q", key, got, want)
			}
		}

		assertField("direction", "in")
		assertField("action", "ACCEPT")
		assertField("protocol", "tcp")
		assertField("source", "0.0.0.0/0")
		assertField("sport", "1024")
		assertField("destination", "10.0.0.1")
		assertField("dport", "22")
		assertField("position", "1")

		enabled, ok := body["enabled"]
		if !ok {
			t.Fatalf("expected enabled field to be present")
		}
		if enabled != true {
			t.Fatalf("enabled = %#v, want true", enabled)
		}
	})

	t.Run("empty rule", func(t *testing.T) {
		rule := firewallRuleModel{
			Direction:   types.StringNull(),
			Action:      types.StringNull(),
			Protocol:    types.StringNull(),
			Source:      types.StringNull(),
			Sport:       types.StringNull(),
			Destination: types.StringNull(),
			Dport:       types.StringNull(),
			Enabled:     types.BoolNull(),
			Position:    types.StringNull(),
		}

		body, diags := expandFirewallRule(rule, 1)
		if diags.HasError() {
			t.Fatalf("expandFirewallRule() returned diagnostics: %v", diags)
		}
		if len(body) != 0 {
			t.Fatalf("expandFirewallRule() len = %d, want 0", len(body))
		}
	})
}
