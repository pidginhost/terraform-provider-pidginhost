# PidginHost Terraform Provider

Manage PidginHost cloud resources from Terraform without touching the web UI. This provider currently ships a full-featured `pidginhost_server` resource that mirrors the behaviour of the internal automation tooling:

- Provision servers with passwords, SSH keys, existing or fresh public IPs, private networking, and destroy protection.
- Create, reuse, or delete firewall rulesets (with optional inline rule seeding).
- Attach a companion volume, grow it in place, or keep/delete it during destroy.
- Control destroy-time behaviour for every attached resource (extra volumes, IPv4, IPv6, firewall rulesets).
- Poll the API for server readiness and capture detailed outputs for downstream usage.

Future releases will add dedicated resources and data sources for catalog, firewalls, volumes, and IP attachments.

---

## Repository layout

```
terraform-provider-pidginhost/
├── build.sh                     # Build helper that wires Go caches and emits bin/terraform-provider-pidginhost
├── internal/provider/           # Provider implementation (Terraform Plugin Framework)
│   ├── client.go                # HTTP API client used by all resources/data sources
│   ├── resource_server.go       # pidginhost_server resource (CRUD + polling + helpers)
│   ├── provider.go              # Provider configuration schema (token, base_url)
│   └── datasource_catalog.go    # (placeholder for upcoming catalog datasource)
├── tf-pidginhost/               # Self-contained example configuration + helper script
│   ├── main.tf                  # Example showing every input and destroy option
│   └── terraform.sh             # Wrapper that generates a temp CLI config with dev_overrides
└── bin/                         # Built provider binaries (populated by build.sh)
```

---

## Requirements

- Go **1.21+** (tested with 1.25.3).
- Terraform **1.4+** (development verified on 1.13.4).
- PidginHost API token with permission to manage servers, firewalls, volumes, and public IPs.
- Python 3 (used by readiness polling scripts in the Terraform module).
- macOS/Linux on `arm64` or `amd64` (adjust Go build flags for other targets).

---

## Building the provider

```bash
git clone https://github.com/pidginhost/terraform-provider-pidginhost.git
cd terraform-provider-pidginhost

# Compile (caches live under ./terraform-provider-pidginhost/.go*)
./build.sh

# Result: bin/terraform-provider-pidginhost
```

`build.sh` prepares isolated Go caches (`.gocache`, `.gomodcache`, `.gotmp`) so repeated builds run quickly without polluting your global Go environment.

---

## Using the provider locally

### Option 1: Temporary CLI override (recommended for development)

```bash
cd terraform-provider-pidginhost
./build.sh
cd tf-pidginhost

# Run Terraform through the wrapper so the dev override is always applied
./terraform.sh init
./terraform.sh plan \
  -var pidginhost_token="$PIDGINHOST_TOKEN" \
  -var server_ssh_pub_key="$(cat ~/.ssh/id_ed25519.pub)"
```

`terraform.sh` writes a temporary CLI configuration that sets:

```hcl
provider_installation {
  dev_overrides {
    "pidginhost/pidginhost" = "<repo>/bin"
  }
}
```

Every Terraform subcommand inherits this override and the script deletes the temp config afterwards.

### Option 2: Manual plugin installation (automation/CI)

```bash
mkdir -p ~/.terraform.d/plugins/pidginhost/pidginhost/0.0.1/<OS>_<ARCH>
cp bin/terraform-provider-pidginhost \
   ~/.terraform.d/plugins/pidginhost/pidginhost/0.0.1/<OS>_<ARCH>/terraform-provider-pidginhost_v0.0.1
```

Replace `<OS>_<ARCH>` with your platform (for example `darwin_arm64`).

---

## Full capability tour

### Server authentication

| Attribute        | Behaviour                                                     |
|------------------|----------------------------------------------------------------|
| `password`       | Plain-text admin password (≤ 50 chars).                        |
| `ssh_pub_key`    | SSH public key material (≤ 3000 chars).                        |
| `ssh_pub_key_id` | Reference to an existing uploaded key.                         |

At least one of these must be supplied.

### Networking & public IP lifecycle

- Attach existing IPv4/IPv6 addresses (`public_ip`, `public_ipv6`).
- Request fresh IPv4/IPv6 allocations with `new_ipv4` / `new_ipv6`.
- Attach a private network, optionally with a static address (`private_network`, `private_address`).
- Decide what happens on destroy with `destroy_public_ipv4` / `destroy_public_ipv6` (delete vs. detach-and-keep).

```hcl
# Reuse both IPs and keep them after destroy (detach only)
resource "pidginhost_server" "web" {
  image     = "ubuntu22"
  package   = "cloudv-3"
  hostname  = "prod-web-01"

  ssh_pub_key           = var.server_ssh_pub_key
  public_ip             = "203.0.113.42"
  public_ipv6           = "2001:db8::feed:beef"
  new_ipv4              = false
  new_ipv6              = false
  destroy_public_ipv4   = false
  destroy_public_ipv6   = false
}

# Mix: keep IPv4, allocate fresh IPv6, delete both IPs when the server is destroyed
resource "pidginhost_server" "api" {
  image     = "ubuntu24"
  package   = "cloudv-4"
  hostname  = "api-01"

  ssh_pub_key           = var.server_ssh_pub_key
  public_ip             = "198.51.100.22"
  new_ipv4              = false
  new_ipv6              = true
  destroy_public_ipv4   = true
  destroy_public_ipv6   = true
}
```

If you only have IPv4, omit `public_ipv6` and leave `new_ipv6` unset (or explicitly `false`). To mix modes (e.g. fresh IPv4, existing IPv6) adjust the `new_*` flags accordingly.

### Firewall rulesets

| Scenario               | Configuration                                                                 |
|------------------------|-------------------------------------------------------------------------------|
| Create new ruleset     | `firewall_rules_create = true`, provide `firewall_rules_set_name`, optional `firewall_rules`. |
| Seed initial rules     | Supply a list of rule objects via `firewall_rules` (only valid when creating a new ruleset). |
| Reuse existing ruleset | `firewall_rules_create = false`, set `fw_rules_set` (slug) or `firewall_rules_set_name`.      |
| Destroy behaviour      | `destroy_firewall_rules_set = true` deletes the ruleset only when Terraform created it.       |

### Extra volume lifecycle

| Action                    | Configuration                                                              |
|---------------------------|----------------------------------------------------------------------------|
| Create & attach volume    | Set `extra_volume_product`, `extra_volume_size`, optional `extra_volume_alias`. |
| Resize or rename volume   | Update `extra_volume_size` / `extra_volume_alias` (handled in place).       |
| Destroy behaviour         | `destroy_extra_volume = true` deletes on destroy; `false` detaches and keeps. |

### Outputs & readiness

- `wait_for_ready_seconds` controls API polling after create/update/destroy.
- Outputs include `server_id`, `server` payload, `extra_volume_id`, `firewall_ruleset_id`, etc., so downstream modules can make use of provisioned assets.

---

## Example workflows

### Create server + brand-new firewall ruleset + new IPs

```hcl
resource "pidginhost_server" "api" {
  image                   = "ubuntu24"
  package                 = "cloudv-4"
  hostname                = "api-01"

  ssh_pub_key             = var.server_ssh_pub_key
  new_ipv4                = true
  new_ipv6                = true
  destroy_public_ipv4     = true
  destroy_public_ipv6     = true

  firewall_rules_create   = true
  firewall_rules_set_name = "api-01-fw"
  firewall_rules = [
    { direction = "in", action = "ACCEPT", protocol = "tcp", dport = "22" },
    { direction = "in", action = "ACCEPT", protocol = "tcp", dport = "443" },
  ]

  extra_volume_product     = "fast-storage"
  extra_volume_size        = 30
  destroy_extra_volume     = true
}
```

Destroying this resource deletes the server, extra volume, firewall ruleset, and both public IPs.

### Reuse shared firewall + keep extra volume + keep IPs

```hcl
resource "pidginhost_server" "web" {
  image      = "ubuntu22"
  package    = "cloudv-3"
  hostname   = "prod-web-01"

  ssh_pub_key            = var.server_ssh_pub_key
  public_ip              = "203.0.113.42"
  new_ipv4               = false
  destroy_public_ipv4    = false

  firewall_rules_create      = false
  firewall_rules_set_name    = "shared-nginx-fw"
  destroy_firewall_rules_set = false

  extra_volume_product   = "fast-storage"
  extra_volume_size      = 50
  destroy_extra_volume   = false  # detach but keep the data disk
}
```

Destroying this resource detaches (but keeps) the extra volume and the IPv4, and leaves the shared firewall untouched.

### Resize extra volume & keep IPs

```hcl
resource "pidginhost_server" "db" {
  image      = "ubuntu24"
  package    = "cloudv-5"
  hostname   = "prod-db-01"

  ssh_pub_key            = var.server_ssh_pub_key
  public_ip              = "198.51.100.42"
  new_ipv4               = false
  destroy_public_ipv4    = false

  firewall_rules_create      = false
  fw_rules_set               = "db-fw-slug"
  destroy_firewall_rules_set = false

  extra_volume_product   = "fast-storage"
  extra_volume_size      = 200
}

# Later: bump the size and the provider calls the resize API in place.
```

### Destroy scenarios cheat sheet

| Setting                         | Effect on destroy                                                |
|---------------------------------|------------------------------------------------------------------|
| `destroy_extra_volume = true`   | Detaches and **deletes** the extra volume.                       |
| `destroy_extra_volume = false`  | Detaches but keeps the extra volume.                             |
| `destroy_public_ipv4 = true`    | Detaches and **deletes** the public IPv4.                        |
| `destroy_public_ipv4 = false`   | Detaches but keeps the public IPv4.                              |
| `destroy_public_ipv6 = true`    | Detaches and **deletes** the public IPv6.                        |
| `destroy_public_ipv6 = false`   | Detaches but keeps the public IPv6.                              |
| `destroy_firewall_rules_set = true` | Deletes the ruleset if Terraform created it. Existing rulesets are never removed automatically. |

---

## Catalog data source outputs (examples)

`tf-pidginhost/main.tf` demonstrates how to expose catalog information for discovery:

```hcl
data "pidginhost_catalog" "catalog" {}

output "available_images" {
  value = jsondecode(data.pidginhost_catalog.catalog.images_json)[*].slug
}

output "available_packages" {
  value = jsondecode(data.pidginhost_catalog.catalog.packages_json)[*].slug
}

output "profile" {
  value = jsondecode(data.pidginhost_catalog.catalog.profile_json)
}

output "volume_products" {
  value = jsondecode(data.pidginhost_catalog.catalog.volume_products_json)[*].slug
}

output "volumes" {
  value = jsondecode(data.pidginhost_catalog.catalog.volumes_json)
}

output "ssh_keys" {
  value = jsondecode(data.pidginhost_catalog.catalog.ssh_keys_json)
}

output "firewalls" {
  value = jsondecode(data.pidginhost_catalog.catalog.firewalls_json)[*].name
}

output "ips" {
  value = jsondecode(data.pidginhost_catalog.catalog.ips_json)
}

output "public_interfaces" {
  value = jsondecode(data.pidginhost_catalog.catalog.public_interfaces_json)
}

output "servers" {
  value = jsondecode(data.pidginhost_catalog.catalog.servers_json)
}
```

Mirror these snippets in your modules when you need to inspect available images, packages, IPs, or other assets before provisioning.

---

## Troubleshooting

| Symptom                                               | Likely cause & remediation                                                                                       |
|-------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `firewall_rules not supported` during plan            | You supplied `firewall_rules` while `firewall_rules_create = false`. Remove the list or set the flag true.        |
| `Object with name=<slug> does not exist`              | The ruleset does not exist server-side. Either create it (`firewall_rules_create = true`) or use a valid slug.    |
| Provider “produced inconsistent result after apply”   | Upgrade to the latest provider build—older revisions cleared `fw_rules_set` when a lookup failed.                 |
| Destroy leaves firewall behind                        | Ensure `destroy_firewall_rules_set = true` **and** the ruleset was created during this Terraform run.             |
| IPs not deleted on destroy                            | Set `destroy_public_ipv4` / `destroy_public_ipv6` to `true` before destroying. Detached IPs remain allocated.     |
| Terraform still downloads from registry               | Use `terraform.sh` (dev override) or place the binary under `~/.terraform.d/plugins/pidginhost/pidginhost/...`.   |

Enable verbose logs with `TF_LOG=TRACE ./terraform.sh apply` when debugging provider/API interactions.

---

## Developing further

1. Run tests:

   ```bash
   GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOTMPDIR=$(pwd)/.gotmp go test ./...
   ```

2. Modify provider code under `internal/provider`.
3. Rebuild via `./build.sh`.
4. Rerun `./terraform.sh init` in `tf-pidginhost` to load the latest binary.

---

## Releasing

1. Ensure `GITHUB_TOKEN` is exported with at least `repo` scope (for example `export GITHUB_TOKEN=$(gh auth token)` after `gh auth login`).
2. Export a GPG key fingerprint that is trusted by GitHub releases (Settings → SSH and GPG keys) under `GPG_FINGERPRINT=<fingerprint>`.
3. Tag the release with a semantic version prefixed with `v` (for example `v1.0.0`), push the tag, and ensure the default branch contains the changelog entry.
4. Run the cross-platform build and publishing pipeline:

   ```bash
   goreleaser release --clean
   ```

   The command produces platform-specific archives named `terraform-provider-pidginhost_<VERSION>_<OS>_<ARCH>.zip`, plus `SHA256SUMS` and `SHA256SUMS.sig`, and uploads them to the corresponding GitHub release draft.
5. Publish the GitHub release, then register the repository under the Terraform Registry UI (`registry.terraform.io`) using the same GitHub organization and repository.

---

## Roadmap

- Dedicated resources/data sources for firewalls, volumes, and IP attachments.
- Expanded catalog data source filters.
- Acceptance tests using the Terraform testing framework.
- Release automation (goreleaser) for cross-platform binaries.

Bug reports and PRs are welcome—please include reproduction steps and logs when reporting issues.
