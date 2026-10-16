# Look a server up by exactly one of id or hostname.
data "pidginhost_server" "web" {
  hostname = "web1.example.com"
}

output "web_ipv4" {
  value = data.pidginhost_server.web.ipv4
}

output "web_private_addresses" {
  value = data.pidginhost_server.web.private_networks[*].address
}
