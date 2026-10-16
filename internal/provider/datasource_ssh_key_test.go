package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	pidginhost "github.com/pidginhost/sdk-go"
)

func testSSHKey(id int32, alias string) pidginhost.SSHKey {
	return pidginhost.SSHKey{Id: id, Alias: &alias, Fingerprint: fmt.Sprintf("SHA256:fp%d", id), Key: "ssh-ed25519 AAAA"}
}

func TestFindSSHKey(t *testing.T) {
	keys := []pidginhost.SSHKey{testSSHKey(1, "laptop"), testSSHKey(2, "ci"), testSSHKey(3, "ci"), {Id: 4, Fingerprint: "SHA256:fp4"}}
	tests := []struct {
		name, attribute, value string
		want                   int32
		err                    string
	}{
		{name: "by id", attribute: "id", value: "2", want: 2},
		{name: "by alias", attribute: "alias", value: "laptop", want: 1},
		{name: "by fingerprint", attribute: "fingerprint", value: "SHA256:fp3", want: 3},
		{name: "empty alias", attribute: "alias", value: "", want: 4},
		{name: "unknown id", attribute: "id", value: "9", err: `no SSH key matches id "9"`},
		{name: "unknown fingerprint", attribute: "fingerprint", value: "SHA256:fp9", err: `no SSH key matches fingerprint "SHA256:fp9"`},
		{name: "alias is not a prefix", attribute: "alias", value: "lap", err: "no SSH key"},
		{name: "shared alias", attribute: "alias", value: "ci", err: "2 SSH keys match alias \"ci\" (ids 2, 3)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findSSHKey(keys, tt.attribute, tt.value)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("got %v, %v; want error %q", got.Id, err, tt.err)
				}
				return
			}
			if err != nil || got.Id != tt.want {
				t.Fatalf("got %v, %v; want %d", got.Id, err, tt.want)
			}
		})
	}
}

func TestSSHKeyDataSourceSearchesEveryPage(t *testing.T) {
	client := fakeRoutesAPI(t, map[string]string{
		"/api/account/ssh-keys/?page=1": `{"count": 2, "next": "more", "results": [
			{"id": 1, "alias": "laptop", "fingerprint": "SHA256:fp1", "key": "ssh-ed25519 AAAA laptop"}]}`,
		"/api/account/ssh-keys/?page=2": `{"count": 2, "next": null, "results": [
			{"id": 2, "alias": "ci", "fingerprint": "SHA256:fp2", "key": "ssh-ed25519 BBBB ci"}]}`,
	})
	state, diags := readDataSource(t, NewSSHKeyDataSource(), client, map[string]tftypes.Value{
		"alias": tftypes.NewValue(tftypes.String, "ci"),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	var got sshKeyModel
	if diags := state.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	want := sshKeyModel{
		ID:          types.StringValue("2"),
		Alias:       types.StringValue("ci"),
		Key:         types.StringValue("ssh-ed25519 BBBB ci"),
		Fingerprint: types.StringValue("SHA256:fp2"),
	}
	if got != want {
		t.Fatalf("got %+v", got)
	}

	_, diags = readDataSource(t, NewSSHKeyDataSource(), client, map[string]tftypes.Value{
		"fingerprint": tftypes.NewValue(tftypes.String, "SHA256:missing"),
	})
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), "no SSH key matches") {
		t.Fatalf("want a not found error, got %v", diags)
	}
}

func TestAccSSHKeyDataSource(t *testing.T) {
	key := testAccSSHPublicKey(t)
	alias := testAccName("key")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "pidginhost_ssh_key" "test" {
  alias = %q
  key   = %q
}

data "pidginhost_ssh_key" "by_alias" {
  alias = pidginhost_ssh_key.test.alias
}

data "pidginhost_ssh_key" "by_fingerprint" {
  fingerprint = pidginhost_ssh_key.test.fingerprint
}

data "pidginhost_ssh_key" "by_id" {
  id = pidginhost_ssh_key.test.id
}
`, alias, key),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.CompareValuePairs("data.pidginhost_ssh_key.by_alias", tfjsonpath.New("id"), "pidginhost_ssh_key.test", tfjsonpath.New("id"), compare.ValuesSame()),
				statecheck.CompareValuePairs("data.pidginhost_ssh_key.by_fingerprint", tfjsonpath.New("alias"), "pidginhost_ssh_key.test", tfjsonpath.New("alias"), compare.ValuesSame()),
				statecheck.ExpectKnownValue("data.pidginhost_ssh_key.by_id", tfjsonpath.New("alias"), knownvalue.StringExact(alias)),
			},
		}},
	})
}
