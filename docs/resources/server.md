---
page_title: "pidginhost_server Resource - PidginHost"
description: |-
  Manages a PidginHost server, optional firewall rules, and an extra volume.
---

# pidginhost_server (Resource)

Provision and manage a PidginHost virtual server, including optional networking attachments, firewall rule sets, and a companion data volume.

## Example Usage

```hcl
resource "pidginhost_server" "example" {
  image    = "ubuntu24"
  package  = "cloudv-3"
  hostname = "example-1"

  ssh_pub_key = file("~/.ssh/id_ed25519.pub")

  new_ipv4                = true
  destroy_public_ipv4     = true
  firewall_rules_create   = true
  firewall_rules_set_name = "example-fw"

  firewall_rules {
    direction = "in"
    action    = "ACCEPT"
    protocol  = "tcp"
    source    = "0.0.0.0/0"
    dport     = "22"
    enabled   = true
  }

  extra_volume_product = "fast-storage"
  extra_volume_size    = 200
}
```

## Argument Reference

### Required

- `image` (String) Image identifier or slug to deploy.
- `package` (String) Package identifier or slug that defines CPU, memory, and base disk.
- `hostname` (String) Hostname assigned to the server.

### Optional

- At least one of `password`, `ssh_pub_key`, or `ssh_pub_key_id` must be supplied to allow access to the created server.
- `destroy_extra_volume` (Boolean) When true the extra volume is deleted on destroy; when false it is detached and retained. Defaults to `true`.
- `destroy_firewall_rules_set` (Boolean) Delete the managed firewall rules set during destroy when it was created by this resource. Defaults to `true`.
- `destroy_public_ipv4` (Boolean) Delete the attached public IPv4 when destroying the resource. Defaults to `false`.
- `destroy_public_ipv6` (Boolean) Delete the attached public IPv6 when destroying the resource. Defaults to `false`.
- `extra_volume_alias` (String) Alias applied to the extra volume. Defaults to `<hostname>-extra`.
- `extra_volume_product` (String) Optional extra volume product slug to provision alongside the server.
- `extra_volume_size` (Number) Size in GiB of the optional extra volume.
- `firewall_rules` (Block List) Optional inline firewall rules applied when creating a new rules set. Each block supports:
  - `action` (String) Action to take (`ACCEPT`, `DROP`, or `REJECT`).
  - `destination` (String) Destination CIDR.
  - `direction` (String) Traffic direction (`in` or `out`).
  - `dport` (String) Destination port or range.
  - `enabled` (Boolean) Whether the rule is enabled.
  - `position` (String) Rule position or order hint.
  - `protocol` (String) Protocol such as `tcp` or `udp`.
  - `source` (String) Source CIDR.
  - `sport` (String) Source port or range.
- `firewall_rules_create` (Boolean) When true, create a dedicated firewall rules set for this server. Defaults to `false`.
- `firewall_rules_set_name` (String) Name to assign to the created or referenced firewall rules set.
- `fw_policy_in` (String) Default firewall policy for inbound traffic.
- `fw_policy_out` (String) Default firewall policy for outbound traffic.
- `fw_rules_set` (String) Existing firewall rules set slug to reuse.
- `new_ipv4` (Boolean) Allocate a new public IPv4 for the server.
- `new_ipv6` (Boolean) Allocate a new public IPv6 for the server.
- `no_network_acknowledged` (Boolean) Set to true to acknowledge creation without public networking.
- `password` (String, Sensitive) Optional administrator password.
- `private_address` (String) Static address to use when attaching a private network.
- `private_network` (String) Private network identifier to attach.
- `project` (String) Optional project label.
- `public_ip` (String) Existing public IPv4 to reuse.
- `public_ipv6` (String) Existing public IPv6 to reuse.
- `ssh_pub_key` (String, Sensitive) SSH public key material to install on the server.
- `ssh_pub_key_id` (String) Identifier of an existing uploaded SSH public key.
- `wait_for_ready_seconds` (Number) Maximum seconds to wait for the server to report as ready. Defaults to `300`.

## Attributes Reference

In addition to the arguments above, the following attributes are exported:

- `firewall_rules_set_id` (String) Identifier of the firewall rules set that is referenced or created.
- `id` (String) Unique identifier assigned to the server.
- `status` (String) Current server status reported by the API.
- `extra_volume_id` (String) Identifier of the optional extra volume.

## Import

Import an existing server by its identifier:

```shell
terraform import pidginhost_server.example 123456
```
