package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	pidginhost "github.com/pidginhost/sdk-go"
)

// Acceptance tests (TF_ACC=1) create real, billed objects on the account the
// token belongs to. Use a dedicated test account.
var testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"pidginhost": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, name := range tokenEnvVars {
		if os.Getenv(name) != "" {
			return
		}
	}
	t.Fatal("acceptance tests need PIDGINHOST_TOKEN")
}

// testAccName returns a unique name for an object created by a test.
func testAccName(kind string) string {
	return fmt.Sprintf("tf-acc-%s-%s", kind, acctest.RandString(8))
}

// testAccSSHPublicKey returns a fresh ed25519 public key in OpenSSH format.
func testAccSSHPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " tf-acc"
}

// testAccForgetAccountKey deletes the account copy of a public key once the
// test ends: creating a server with ssh_pub_key also stores the key on the
// account, and nothing else would remove it.
func testAccForgetAccountKey(t *testing.T, key string) {
	t.Helper()
	// Only acceptance runs reach the API; a unit run must never use a token.
	if os.Getenv("TF_ACC") == "" {
		return
	}
	t.Cleanup(func() {
		token := ""
		for _, name := range tokenEnvVars {
			if token == "" {
				token = os.Getenv(name)
			}
		}
		client := newAPIClient(token, defaultBaseURL, "terraform-provider-pidginhost/test")
		ctx := context.Background()
		keys, err := fetchAll(func(page int32) ([]pidginhost.SSHKey, bool, error) {
			list, _, err := client.AccountAPI.AccountSshKeysList(ctx).Page(page).Execute()
			if err != nil {
				return nil, false, err
			}
			return list.Results, list.GetNext() != "", nil
		})
		if err != nil {
			t.Errorf("listing account keys for cleanup: %s", describeError(err))
			return
		}
		for _, k := range keys {
			if sshKeyBody(k.Key) == sshKeyBody(key) {
				if _, err := client.AccountAPI.AccountSshKeysDestroy(ctx, strconv.Itoa(int(k.Id))).Execute(); err != nil {
					t.Errorf("deleting account key %d: %s", k.Id, describeError(err))
				}
			}
		}
	})
}
