resource "pidginhost_server_snapshot" "before_upgrade" {
  server_id   = pidginhost_server.web.id
  name        = "before-upgrade"
  description = "Taken before the OS upgrade"
}
