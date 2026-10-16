data "pidginhost_kubernetes_cluster_types" "all" {}

locals {
  dev = one([for t in data.pidginhost_kubernetes_cluster_types.all.cluster_types : t if t.type == "dev"])
}

output "dev_worker_packages" {
  value = local.dev.worker_node_packages[*].slug
}
