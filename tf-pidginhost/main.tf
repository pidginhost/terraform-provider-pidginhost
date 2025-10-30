terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "0.0.1"
    }
  }
}
provider "pidginhost" {
  # Or export PIDGINHOST_TOKEN beforehand
  token = var.pidginhost_token
}

variable "pidginhost_token" {
  type      = string
  sensitive = true
}

variable "firewall_rules_set_name" {
  type    = string
  default = "tf-example-fw"
}

variable "firewall_rules_create" {
  type    = bool
  default = true
}

variable "firewall_rules" {
  type = list(object({
    direction   = optional(string)
    action      = optional(string)
    protocol    = optional(string)
    source      = optional(string)
    sport       = optional(string)
    destination = optional(string)
    dport       = optional(string)
    enabled     = optional(bool)
    position    = optional(string)
  }))
  default = [
    {
      direction = "in"
      action    = "ACCEPT"
      protocol  = "tcp"
      dport     = "22"
    }
  ]
}

variable "destroy_firewall_rules_set" {
  type    = bool
  default = true
}

variable "server_ssh_pub_key" {
  type    = string
  default = null
}

variable "server_password" {
  type      = string
  sensitive = true
  default   = null
}

variable "server_public_ip" {
  type    = string
  default = null
}

variable "server_public_ipv6" {
  type    = string
  default = null
}

variable "server_new_ipv4" {
  type    = bool
  default = true
}

variable "server_new_ipv6" {
  type    = bool
  default = true
}

variable "server_destroy_public_ipv4" {
  type    = bool
  default = false
}

variable "server_destroy_public_ipv6" {
  type    = bool
  default = false
}

variable "destroy_public_ipv4" {
  type    = bool
  default = null
}

variable "destroy_public_ipv6" {
  type    = bool
  default = null
}

resource "pidginhost_server" "web1" {
  image                  = "ubuntu22"
  package                = "cloudv-3"
  hostname               = "tf-example-2"
  ssh_pub_key            = var.server_ssh_pub_key
  password               = var.server_password
  wait_for_ready_seconds = 300

  public_ip             = var.server_public_ip
  public_ipv6           = var.server_public_ipv6
  new_ipv4              = var.server_new_ipv4
  new_ipv6              = var.server_new_ipv6
  destroy_public_ipv4   = coalesce(var.destroy_public_ipv4, var.server_destroy_public_ipv4)
  destroy_public_ipv6   = coalesce(var.destroy_public_ipv6, var.server_destroy_public_ipv6)

  extra_volume_product       = "fast-storage"
  extra_volume_size          = 160
  destroy_extra_volume       = true
  firewall_rules_set_name    = var.firewall_rules_set_name
  firewall_rules_create      = var.firewall_rules_create
  firewall_rules             = var.firewall_rules_create ? var.firewall_rules : []
  destroy_firewall_rules_set = var.destroy_firewall_rules_set
}

#resource "pidginhost_server" "web2" {
#image                  = "ubuntu22"
#package                = "cloudv-3"
#hostname               = "tf-example-2"
#ssh_pub_key            = var.server_ssh_pub_key
#password               = var.server_password
#wait_for_ready_seconds = 300
#
#extra_volume_product       = "fast-storage"
#extra_volume_size          = 30
#destroy_extra_volume       = false # keep this volume on destroy
#new_ipv4                   = true
#firewall_rules_create      = var.firewall_rules_create
#firewall_rules_set_name    = var.firewall_rules_set_name
#firewall_rules             = var.firewall_rules
#destroy_firewall_rules_set = var.destroy_firewall_rules_set
#}

data "pidginhost_catalog" "catalog" {}
#
#output "available_images" {
#value = jsondecode(data.pidginhost_catalog.catalog.images_json)[*].slug
#}
#
#output "available_packages" {
#value = jsondecode(data.pidginhost_catalog.catalog.packages_json)[*].slug
#}
#
#output "profile" {
#value = jsondecode(data.pidginhost_catalog.catalog.profile_json)
#}
#
#output "volume_products" {
#value = jsondecode(data.pidginhost_catalog.catalog.volume_products_json)[*].slug
#}
#
#output "volumes" {
#value = jsondecode(data.pidginhost_catalog.catalog.volumes_json)
#}
#
#output "ssh_keys" {
#value = jsondecode(data.pidginhost_catalog.catalog.ssh_keys_json)
#}
#
output "firewalls" {
  value = jsondecode(data.pidginhost_catalog.catalog.firewalls_json)[*].name
}
#
#output "ips" {
#value = jsondecode(data.pidginhost_catalog.catalog.ips_json)
#}
#
#output "public_interfaces" {
#value = jsondecode(data.pidginhost_catalog.catalog.public_interfaces_json)
#}
#
#output "servers" {
#value = jsondecode(data.pidginhost_catalog.catalog.servers_json)
#}
