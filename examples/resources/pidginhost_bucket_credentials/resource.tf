resource "pidginhost_bucket" "media" {
  name     = "media"
  quota_gb = 10
}

# Reads the bucket's current keys. Changing rotation_triggers rotates them.
resource "pidginhost_bucket_credentials" "media" {
  bucket_id = pidginhost_bucket.media.id

  rotation_triggers = {
    rotated = "2026-10"
  }
}

output "media_secret_key" {
  value     = pidginhost_bucket_credentials.media.secret_key
  sensitive = true
}
