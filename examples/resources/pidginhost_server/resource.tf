resource "pidginhost_ssh_key" "admin" {
  alias = "admin"
  key   = file("~/.ssh/id_ed25519.pub")
}

variable "password" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "pidginhost_server" "web" {
  hostname       = "web1"
  image          = "ubuntu24"
  package        = "cloudv-1"
  ssh_pub_key_id = pidginhost_ssh_key.admin.id
  new_ipv4       = true

  # Never saved in the state. Raise the version to send a new password.
  password_wo         = var.password
  password_wo_version = 1

  fw_policy_in  = "DROP"
  fw_policy_out = "ACCEPT"

  destroy_public_ipv4 = true

  timeouts {
    create = "60m"
  }
}

output "web_ipv4" {
  value = pidginhost_server.web.ipv4_address
}
