resource "pidginhost_public_ip" "web" {
  version          = 4
  server_id        = pidginhost_server.example.id
  reverse_dns      = "www.example.com"
  reboot_on_attach = true
}
