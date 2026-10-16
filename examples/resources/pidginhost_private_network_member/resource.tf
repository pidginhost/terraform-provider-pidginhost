resource "pidginhost_private_network" "backend" {
  address = "198.51.100.0/24"
}

resource "pidginhost_private_network_member" "db" {
  network_id = pidginhost_private_network.backend.id
  server_id  = pidginhost_server.db.id
  address    = "198.51.100.10"
}
