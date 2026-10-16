resource "pidginhost_kubernetes_cluster" "example" {
  name                  = "example"
  cluster_type          = "prod"
  resource_pool_package = "k8s-w2"
  resource_pool_size    = 3
  features              = ["cert-manager", "metrics-server"]

  # Destroying a protected cluster needs protected = false applied first.
  protected = true

  timeouts {
    create = "60m"
  }
}
