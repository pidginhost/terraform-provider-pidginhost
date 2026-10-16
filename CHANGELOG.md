# Changelog

## [Unreleased]

### Added

- Resources for volumes, firewalls, public and floating IPs, private networks, buckets and their keys, snapshots and SSH keys.
- Kubernetes clusters with routes, port forwards, a load balancer firewall and connected servers, plus a kubeconfig data source.
- Data sources for images, packages, generations, storage products, Kubernetes cluster types, SSH keys and servers.
- Servers resize in place, and their password, SSH key, project, firewall and destroy protection change without replacing them.
- Every resource with a wait takes a timeouts block.

### Changed

- pidginhost_server no longer creates firewall rule sets or extra volumes. Read the 0.2 upgrade guide before upgrading.
- The server package must be its slug, not its numeric id.

### Deprecated

- pidginhost_catalog: use the typed data sources instead.

### Fixed

- Empty Kubernetes firewall configurations apply without repeated changes, and interrupted creation stays tracked by Terraform.
- Public addresses can be destroyed after detachment, and volume moves finish once the target server can use them.
- Terraform refuses to move public addresses whose current owner is unknown.
- Snapshots stay managed until deletion finishes.
- Credential errors no longer expose response contents.
- Terraform refuses to create or change the platform's Kubernetes API route.
- Kubernetes first-pool errors point to the control panel for resizing.
- Changes to cluster gateway settings now request replacement instead of appearing to succeed without effect.
- Terraform installs the provider with the plugin protocol it actually speaks.
- Listings return every page instead of only the first.
- API errors name the field the API refused.

## [0.1.2] - 2025-10-31

- First registry release with pidginhost_server and pidginhost_catalog.
