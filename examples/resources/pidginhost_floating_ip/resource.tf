resource "pidginhost_floating_ip" "lb" {
  version     = 4
  label       = "lb"
  reverse_dns = "lb.example.com"

  authorized_server_ids = [
    pidginhost_server.lb1.id,
    pidginhost_server.lb2.id,
  ]
}
