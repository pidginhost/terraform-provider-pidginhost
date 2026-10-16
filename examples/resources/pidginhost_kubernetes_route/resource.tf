resource "pidginhost_kubernetes_route" "web" {
  cluster_id           = pidginhost_kubernetes_cluster.example.id
  protocol             = "http"
  name                 = "web"
  hostnames            = ["example.com", "www.example.com"]
  backend_service_name = "web"
  backend_service_port = 80
}

resource "pidginhost_kubernetes_route" "dns" {
  cluster_id           = pidginhost_kubernetes_cluster.example.id
  protocol             = "udp"
  name                 = "dns"
  port                 = 5353
  backend_service_name = "dns"
  backend_service_port = 53
}
