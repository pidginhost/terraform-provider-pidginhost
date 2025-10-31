---
page_title: "pidginhost_catalog Data Source - PidginHost"
description: |-
  Fetches catalog details from the PidginHost API.
---

# pidginhost_catalog (Data Source)

Query the PidginHost catalog to discover available images, packages, volume products, IP addresses, and other account-scoped assets.

## Example Usage

```hcl
data "pidginhost_catalog" "current" {}

locals {
  image_slugs = [for image in jsondecode(data.pidginhost_catalog.current.images_json) : image.slug]
}
```

## Attributes Reference

All attributes are exported as JSON strings that can be decoded with `jsondecode`:

- `firewalls_json` (String) JSON array describing firewall rule sets.
- `images_json` (String) JSON array describing available server images.
- `ips_json` (String) JSON array describing public IP addresses.
- `packages_json` (String) JSON array describing available packages.
- `profile_json` (String) JSON object containing account profile information.
- `public_interfaces_json` (String) JSON array describing public interfaces.
- `servers_json` (String) JSON array describing servers managed by the account.
- `ssh_keys_json` (String) JSON array describing uploaded SSH keys.
- `volume_products_json` (String) JSON array describing available volume products.
- `volumes_json` (String) JSON array describing volumes managed by the account.
