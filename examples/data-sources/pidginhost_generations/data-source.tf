data "pidginhost_generations" "all" {}

output "default_generation" {
  value = one([for g in data.pidginhost_generations.all.generations : g.slug if g.is_default == true])
}
