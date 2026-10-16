resource "pidginhost_ssh_key" "admin" {
  alias = "admin"
  key   = file("~/.ssh/id_ed25519.pub")
}
