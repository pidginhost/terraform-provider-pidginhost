package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
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
