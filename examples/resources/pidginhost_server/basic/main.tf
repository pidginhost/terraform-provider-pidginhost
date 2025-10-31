terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "~> 1.0.0"
    }
  }
}

variable "pidginhost_token" {
  description = "API token with permission to manage PidginHost servers."
  type        = string
  sensitive   = true
}

provider "pidginhost" {
  token = var.pidginhost_token
}

resource "pidginhost_server" "example" {
  image    = "ubuntu24"
  package  = "cloudv-3"
  hostname = "example-1"

  ssh_pub_key = file("~/.ssh/id_ed25519.pub")

  new_ipv4            = true
  destroy_public_ipv4 = true

  firewall_rules_create   = true
  firewall_rules_set_name = "example-fw"

  firewall_rules {
    direction = "in"
    action    = "ACCEPT"
    protocol  = "tcp"
    source    = "0.0.0.0/0"
    dport     = "22"
    enabled   = true
  }

  extra_volume_product = "fast-storage"
  extra_volume_size    = 200
}
