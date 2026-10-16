# Deprecated: kept for configurations written for provider 0.1.x. Prefer the
# typed data sources, such as pidginhost_images and pidginhost_packages.
data "pidginhost_catalog" "current" {}

output "image_slugs" {
  value = [for image in jsondecode(data.pidginhost_catalog.current.images_json) : image.slug]
}
