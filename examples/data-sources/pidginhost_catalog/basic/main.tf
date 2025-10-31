terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "~> 1.0.0"
    }
  }
}

variable "pidginhost_token" {
  description = "API token with permission to read catalog information."
  type        = string
  sensitive   = true
}

provider "pidginhost" {
  token = var.pidginhost_token
}

data "pidginhost_catalog" "current" {}

output "image_slugs" {
  value = [for image in jsondecode(data.pidginhost_catalog.current.images_json) : image.slug]
}
