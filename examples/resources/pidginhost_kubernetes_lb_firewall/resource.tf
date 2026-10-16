# Lists every rule of the cluster load balancer: rules missing here are deleted.
resource "pidginhost_kubernetes_lb_firewall" "example" {
  cluster_id = pidginhost_kubernetes_cluster.example.id

  rule {
    direction = "in"
    action    = "ACCEPT"
    protocol  = "tcp"
    source    = "203.0.113.0/24"
    dport     = "443"
    comment   = "office"
  }

  rule {
    direction = "in"
    action    = "DROP"
    protocol  = "tcp"
    dport     = "443"
    position  = 200
  }
}
