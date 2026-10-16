terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "~> 0.2"
    }
  }
}

# Reads the token from PIDGINHOST_TOKEN when token is not set here.
provider "pidginhost" {}
