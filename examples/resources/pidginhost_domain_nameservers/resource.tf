resource "pidginhost_domain_nameservers" "example" {
  domain      = "example.com"
  nameservers = ["ns1.example.net", "ns2.example.net"]
}
