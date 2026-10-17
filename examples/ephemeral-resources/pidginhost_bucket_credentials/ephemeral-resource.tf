ephemeral "pidginhost_bucket_credentials" "media" {
  bucket_id = var.bucket_id
}

# An S3 client configured without saving the keys in the state.
provider "aws" {
  region     = "us-east-1"
  access_key = ephemeral.pidginhost_bucket_credentials.media.access_key
  secret_key = ephemeral.pidginhost_bucket_credentials.media.secret_key

  endpoints {
    s3 = var.bucket_endpoint
  }
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_region_validation      = true
  skip_requesting_account_id  = true
}
