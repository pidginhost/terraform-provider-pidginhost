package provider

import (
	"os"
	"testing"
)

// testAccDNSDomain returns PIDGINHOST_ACC_DNS_DOMAIN, a domain registered
// with PidginHost on the test account, and skips the test when it is unset:
// the account may have no domain to test with.
func testAccDNSDomain(t *testing.T) string {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests run only with TF_ACC set")
	}
	domain := os.Getenv("PIDGINHOST_ACC_DNS_DOMAIN")
	if domain == "" {
		t.Skip("set PIDGINHOST_ACC_DNS_DOMAIN to a domain registered on the test account")
	}
	return domain
}

func TestFqdn(t *testing.T) {
	for in, want := range map[string]string{
		" Example.COM. ":           "example.com",
		"example.com..":            "example.com",
		"_dmarc.Example.com":       "_dmarc.example.com",
		"bücher.example":           "xn--bcher-kva.example",
		"_sip._tcp.bücher.example": "_sip._tcp.xn--bcher-kva.example",
		"Straße.example":           "strasse.example",
		"":                         "",
	} {
		if got := fqdn(in); got != want {
			t.Errorf("fqdn(%q) = %q, want %q", in, got, want)
		}
	}
}
