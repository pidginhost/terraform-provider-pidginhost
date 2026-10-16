data "pidginhost_packages" "gen2" {
  generation = "gen2"
}

locals {
  # First package with at least 4 GB of memory.
  small = [for p in data.pidginhost_packages.gen2.packages : p if p.memory >= 4][0]
}

output "package_slug" {
  value = local.small.slug
}
