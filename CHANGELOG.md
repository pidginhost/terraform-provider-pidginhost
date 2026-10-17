# Changelog

## [Unreleased]

### Added

- Resources for volumes, firewalls, public and floating IPs, private networks, buckets and their keys, snapshots and SSH keys.
- Kubernetes clusters with node pools, routes, port forwards, a load balancer firewall and connected servers, plus a kubeconfig data source.
- Data sources for images, packages, generations, storage products, Kubernetes cluster types, SSH keys and servers.
- FreeDNS zones and records, and the name servers and glue records of domains registered with PidginHost.
- Servers resize in place, and their password, SSH key, project, firewall and destroy protection change without replacing them.
- Every resource that waits for the API takes a timeouts block.

### Changed

- pidginhost_server no longer creates firewall rule sets or extra volumes. Read the 0.2 upgrade guide before upgrading.
- The server package must be its slug, not its numeric id.

### Deprecated

- pidginhost_catalog: use the typed data sources instead.

### Fixed

- DNS lifetime changes no longer report errors after a successful edit.
- Imported mail and service records keep their root targets when their DNS lifetimes change.
- Already inactive DNS zones can be destroyed without an error.
- Concurrent worker changes wait for each other, and removals report failures before an apply finishes.
- Failed worker and volume creation preserves deletion settings for cleanup.
- DNS records can be destroyed after their zone is deactivated.
- Invalid worker counts and DNS lifetimes are rejected before an apply changes them.
- Terraform installs the provider with the plugin protocol it actually speaks.
- Listings return every page instead of only the first.
- API errors name the field the API refused.

## [0.1.2] - 2025-10-31

- First registry release with pidginhost_server and pidginhost_catalog.
