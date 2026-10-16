data "pidginhost_kubernetes_kubeconfig" "example" {
  cluster_id = pidginhost_kubernetes_cluster.example.id
}

# The kubeconfig holds cluster admin credentials.
resource "local_sensitive_file" "kubeconfig" {
  filename        = "${path.module}/kubeconfig"
  content         = data.pidginhost_kubernetes_kubeconfig.example.kubeconfig
  file_permission = "0600"
}
