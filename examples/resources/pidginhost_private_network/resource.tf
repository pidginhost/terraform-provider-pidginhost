resource "pidginhost_private_network" "backend" {
  address = "198.51.100.0/24"
  gateway = "198.51.100.1"
}
