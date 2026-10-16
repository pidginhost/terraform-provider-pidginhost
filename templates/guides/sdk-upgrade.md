---
page_title: "Upgrading from 0.1.x"
description: |-
  Keep existing servers while adapting a 0.1.x configuration to the SDK provider.
---

# Upgrading from 0.1.x

Back up Terraform state before upgrading. Protect the backup because state can contain credentials.
The provider upgrades existing server state automatically and keeps the server ID. Do not remove or
reimport the server to upgrade it. Review the plan before applying; unchanged server creation settings
should not request replacement.

## Configuration changes

- Remove `firewall_rules_create`, `firewall_rules_set_name`, `firewall_rules`, and `destroy_firewall_rules_set`.
  To keep selecting an existing firewall, set `fw_rules_set` to its name.
- Remove `extra_volume_product`, `extra_volume_alias`, `extra_volume_size`, and `destroy_extra_volume`.
- Remove references to the old `extra_volume_id` and `firewall_rules_set_id` outputs.
- Replace `wait_for_ready_seconds` with a `timeouts` block. The default create and update timeout is
  45 minutes; the default delete timeout is 30 minutes.
- Remove `pidginhost_catalog` and its output references. Obtain image and package slugs through the API or panel.
- Use package slugs and firewall names rather than numeric IDs for `package` and `fw_rules_set`.

## Existing attachments

An extra volume created by 0.1.x stays attached. A firewall set stays on the account and remains applied.
The upgrade warning identifies these objects. This provider no longer edits or deletes them, including
when the server is destroyed. Manage them separately through the API or panel.

Dedicated volume, firewall, and public IP resources are not available in this version. Do not attempt to
import those objects into resource types that the provider does not register.

The `destroy_public_ipv4` and `destroy_public_ipv6` settings keep their existing values. Set them before
destroy if the provider should release the server's public addresses. Check any cleanup warnings.

## State and planning

The server `status` output now reports the API resource status, such as `active`, rather than machine status.
Package changes now resize in place. Omitted optional computed settings keep their current API values;
explicit empty values clear project labels, SSH login keys, or firewall associations.

Destroy protection is refreshed from the API. To delete a protected server, set `destroy_protection = false`
and apply before running destroy. Successful creation records the enabled protection state.
