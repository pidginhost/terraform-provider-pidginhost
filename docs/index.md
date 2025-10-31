---
page_title: "PidginHost Provider"
description: |-
  Manage PidginHost compute, networking, and storage resources from Terraform.
---

# PidginHost Provider

Use the PidginHost provider to provision and manage servers, firewall rule sets, public IPs, and companion volumes through the PidginHost public API.

## Example Usage

```hcl
terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "~> 1.0.0"
    }
  }
}

provider "pidginhost" {
  token = var.pidginhost_token
}
```

```hcl
resource "pidginhost_server" "example" {
  image    = "ubuntu24"
  package  = "cloudv-3"
  hostname = "example-1"

  ssh_pub_key = file("~/.ssh/id_ed25519.pub")

  firewall_rules_create  = true
  firewall_rules_set_name = "example-fw"

  firewall_rules {
    direction = "in"
    action    = "ACCEPT"
    protocol  = "tcp"
    source    = "0.0.0.0/0"
    dport     = "22"
    enabled   = true
  }
}
```

## Authentication

Supply your PidginHost API token either through the provider configuration or by exporting the `PIDGINHOST_TOKEN` environment variable. When both are present, the explicit provider configuration value wins.

## Argument Reference

### Optional

- `token` (String, Sensitive) PidginHost API token. May also be sourced from the `PIDGINHOST_TOKEN` environment variable.
- `base_url` (String) Base URL for the PidginHost API. Defaults to `https://www.pidginhost.com` and rarely needs to be overridden.

## Environment Variables

The provider reads `PIDGINHOST_TOKEN` for authentication when the `token` argument is not set.
