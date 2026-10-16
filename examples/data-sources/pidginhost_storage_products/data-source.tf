data "pidginhost_storage_products" "all" {}

output "storage_products" {
  value = { for p in data.pidginhost_storage_products.all.storage_products : p.slug => "${p.min_size}-${p.max_size} ${p.unit}" }
}
