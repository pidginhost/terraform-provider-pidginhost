data "pidginhost_images" "all" {}

locals {
  ubuntu = one([for image in data.pidginhost_images.all.images : image if image.slug == "ubuntu24"])
}

output "ubuntu_login" {
  value = local.ubuntu.default_username
}
