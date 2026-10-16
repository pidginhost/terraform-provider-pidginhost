resource "pidginhost_bucket" "media" {
  name        = "media"
  quota_gb    = 10
  public_read = false
}

output "media_bucket" {
  value = {
    name     = pidginhost_bucket.media.full_name
    endpoint = pidginhost_bucket.media.endpoint
    region   = pidginhost_bucket.media.region
  }
}
