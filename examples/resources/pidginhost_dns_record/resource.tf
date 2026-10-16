resource "pidginhost_dns_zone" "example" {
  domain = "example.com"
  ip     = "192.0.2.10"
}

resource "pidginhost_dns_record" "www" {
  zone    = pidginhost_dns_zone.example.domain
  name    = "www"
  type    = "A"
  ttl     = 3600
  address = "192.0.2.10"
}

resource "pidginhost_dns_record" "mail" {
  zone       = pidginhost_dns_zone.example.domain
  name       = "@"
  type       = "MX"
  preference = 10
  exchange   = "mail.example.com"
}

resource "pidginhost_dns_record" "spf" {
  zone    = pidginhost_dns_zone.example.domain
  name    = "@"
  type    = "TXT"
  txtdata = "v=spf1 mx -all"
}

resource "pidginhost_dns_record" "caa" {
  zone  = pidginhost_dns_zone.example.domain
  name  = "@"
  type  = "CAA"
  flag  = 0
  tag   = "issue"
  value = "letsencrypt.org"
}
