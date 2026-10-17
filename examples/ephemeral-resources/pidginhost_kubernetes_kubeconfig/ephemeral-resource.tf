ephemeral "pidginhost_kubernetes_kubeconfig" "example" {
  cluster_id = var.cluster_id
}

locals {
  kubeconfig = yamldecode(ephemeral.pidginhost_kubernetes_kubeconfig.example.kubeconfig)
  cluster    = local.kubeconfig.clusters[0].cluster
  user       = local.kubeconfig.users[0].user
}

# The credentials reach the provider without being saved in the state.
provider "kubernetes" {
  host                   = local.cluster.server
  cluster_ca_certificate = base64decode(local.cluster["certificate-authority-data"])
  client_certificate     = try(base64decode(local.user["client-certificate-data"]), null)
  client_key             = try(base64decode(local.user["client-key-data"]), null)
  token                  = try(local.user.token, null)
}
