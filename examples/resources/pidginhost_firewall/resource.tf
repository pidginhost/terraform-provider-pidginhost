resource "pidginhost_firewall" "web" {
  name = "web"

  rule = [
    {
      direction = "in"
      action    = "ACCEPT"
      protocol  = "tcp"
      source    = "192.0.2.0/24"
      dport     = "22"
    },
    {
      direction = "in"
      action    = "ACCEPT"
      protocol  = "tcp"
      dport     = "80,443"
    },
  ]
}
