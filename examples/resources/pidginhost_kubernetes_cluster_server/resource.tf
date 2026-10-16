# Connecting the server reboots it. The cluster needs cloud VM access enabled
# in the control panel.
resource "pidginhost_kubernetes_cluster_server" "db" {
  cluster_id = pidginhost_kubernetes_cluster.example.id
  server_id  = "12"
}
