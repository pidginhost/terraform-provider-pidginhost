resource "pidginhost_volume" "data" {
  server_id = pidginhost_server.example.id
  alias     = "data"
  product   = "fast-storage"
  size      = 100
}
