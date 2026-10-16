resource "pidginhost_domain_glue_record" "ns1" {
  domain = "example.com"
  name   = "ns1"
  ip     = "192.0.2.53"
  ip2    = "2001:db8::53"
}
