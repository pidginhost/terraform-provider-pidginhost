resource "pidginhost_kubernetes_node_pool" "workers" {
  cluster_id = pidginhost_kubernetes_cluster.example.id
  package    = "k8s-4"
  size       = 3

  # Required to shrink the pool or destroy it while it has workers: the
  # removed nodes lose emptyDir and local volume data.
  local_data_loss_accepted = true
}
