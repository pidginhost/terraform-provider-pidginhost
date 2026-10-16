resource "pidginhost_kubernetes_port_forward" "ssh" {
  cluster_id  = pidginhost_kubernetes_cluster.example.id
  port        = 2222
  protocol    = "tcp"
  internal_ip = "192.0.2.10"
}
