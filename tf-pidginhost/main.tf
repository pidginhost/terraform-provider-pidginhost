# Local development sample. Run it through ./terraform.sh after building the
# provider into ../bin (see the README), with PIDGINHOST_TOKEN exported.
terraform {
  required_providers {
    pidginhost = {
      source = "pidginhost/pidginhost"
    }
  }
}

provider "pidginhost" {}

variable "ssh_public_key" {
  type = string
}

data "pidginhost_images" "all" {}

resource "pidginhost_ssh_key" "dev" {
  alias = "tf-dev"
  key   = var.ssh_public_key
}

resource "pidginhost_firewall" "dev" {
  name = "tf-dev"

  rule {
    direction = "in"
    action    = "ACCEPT"
    protocol  = "tcp"
    dport     = "22"
  }
}

resource "pidginhost_server" "dev" {
  hostname       = "tf-dev"
  image          = "ubuntu24"
  package        = "cloudv-1"
  ssh_pub_key_id = pidginhost_ssh_key.dev.id
  new_ipv4       = true
  fw_rules_set   = pidginhost_firewall.dev.name
  fw_policy_in   = "DROP"

  destroy_public_ipv4 = true
}

output "ipv4" {
  value = pidginhost_server.dev.ipv4_address
}
