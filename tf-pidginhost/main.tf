terraform {
  required_providers {
    pidginhost = {
      source = "pidginhost/pidginhost"
    }
  }
}

provider "pidginhost" {}

variable "server_ssh_pub_key" {
  description = "SSH public key in OpenSSH format."
  type        = string
}

resource "pidginhost_server" "web1" {
  hostname            = "web1"
  image               = "ubuntu-24-04"
  package             = "cloudv-1"
  ssh_pub_key         = var.server_ssh_pub_key
  new_ipv4            = true
  destroy_public_ipv4 = true

  timeouts {
    create = "60m"
  }
}

output "server_id" {
  value = pidginhost_server.web1.id
}

output "ipv4_address" {
  value = pidginhost_server.web1.ipv4_address
}
