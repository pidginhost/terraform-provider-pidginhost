# PidginHost Terraform Provider

Manage PidginHost cloud resources with Terraform.

| Area | Resources | Data sources |
|---|---|---|
| Servers | `pidginhost_server`, `pidginhost_server_snapshot`, `pidginhost_ssh_key` | `pidginhost_server`, `pidginhost_ssh_key`, `pidginhost_images`, `pidginhost_packages`, `pidginhost_generations` |
| Storage | `pidginhost_volume`, `pidginhost_bucket`, `pidginhost_bucket_credentials` | `pidginhost_storage_products` |
| Networking | `pidginhost_firewall`, `pidginhost_public_ip`, `pidginhost_floating_ip`, `pidginhost_private_network`, `pidginhost_private_network_member` | |
| Kubernetes | `pidginhost_kubernetes_cluster`, `pidginhost_kubernetes_route`, `pidginhost_kubernetes_port_forward`, `pidginhost_kubernetes_lb_firewall`, `pidginhost_kubernetes_cluster_server` | `pidginhost_kubernetes_cluster_types`, `pidginhost_kubernetes_kubeconfig` |

`pidginhost_catalog` from 0.1 still works but is deprecated. Read the
[0.2 upgrade guide](docs/guides/upgrading-to-0.2.md) before updating a configuration written for 0.1.

## Requirements

- Terraform 1.4 or newer.
- A PidginHost API token with permission to manage the resources you use.
- Go 1.27.2 or newer to build the provider yourself.

## Usage

```hcl
terraform {
  required_providers {
    pidginhost = {
      source  = "pidginhost/pidginhost"
      version = "~> 0.2"
    }
  }
}

provider "pidginhost" {}

resource "pidginhost_ssh_key" "admin" {
  alias = "admin"
  key   = file("~/.ssh/id_ed25519.pub")
}

resource "pidginhost_firewall" "web" {
  name = "web"
  rule = [
    { direction = "in", action = "ACCEPT", protocol = "tcp", dport = "22" },
    { direction = "in", action = "ACCEPT", protocol = "tcp", dport = "80,443" },
  ]
}

resource "pidginhost_server" "web" {
  hostname            = "web1"
  image               = "ubuntu-24-04"
  package             = "cloudv-1"
  ssh_pub_key_id      = pidginhost_ssh_key.admin.id
  new_ipv4            = true
  fw_rules_set        = pidginhost_firewall.web.name
  fw_policy_in        = "DROP"
  destroy_public_ipv4 = true

  timeouts {
    create = "60m"
  }
}
```

Supply the token through `PIDGINHOST_TOKEN`, or pass a sensitive variable to the provider's `token`
argument. `PIDGINHOST_ACCESS_TOKEN` and `PIDGINHOST_API_TOKEN` are read too, in that order.
Image and package slugs come from the `pidginhost_images` and `pidginhost_packages` data sources.
The [documentation](docs/index.md) lists every argument and import id.

## Lifecycle

Package changes resize the server in place. The API rejects packages with a smaller disk.
Terraform refreshes CPU, memory, and disk outputs after a resize.

Set `destroy_protection = false` and apply before destroying a protected server.
Set `destroy_public_ipv4` and `destroy_public_ipv6` before destroy if those addresses should be released.
Otherwise they remain on the account and may incur charges. Check destroy warnings for any address
that could not be released.

Omitted optional computed settings keep the API's current values. Use an empty `project`, `ssh_pub_key`,
or `fw_rules_set` to clear that setting. Clearing a login key does not revoke keys already installed in the guest.
Changing an SSH login key can reboot the server.

Passwords, startup scripts, and provider credentials are sensitive. Protect Terraform state and saved plans.
Errors while fetching bucket credentials or kubeconfig withhold response details that could contain secrets.
The API stores startup scripts in cleartext.

## Local development

```sh
go build -o bin/terraform-provider-pidginhost .
go test ./... -skip '^TestAcc'
go vet ./...
golangci-lint run ./...
go generate ./...
```

The unit suite uses a fake HTTP API. Its Terraform lifecycle test uses `TF_ACC_TERRAFORM_PATH`
when set, or downloads a Terraform CLI. Acceptance tests create billed resources and require
`TF_ACC=1` plus account credentials; run them only against a dedicated test account.

The `tf-pidginhost` example and its `terraform.sh` wrapper use the binary in `bin` through a local
development override. With a Terraform CLI on `PATH`, run `./terraform.sh plan` from that directory.
The wrapper creates a temporary CLI configuration and removes it on exit.

Resource schemas are in `internal/provider`. `sdk-go` handles API calls. Run `go generate ./...`
after changing schema descriptions or examples and commit the generated `docs` output.

## Releases

The GoReleaser configuration builds platform archives, includes the registry manifest, and signs checksums
with the GPG fingerprint selected by `GPG_FINGERPRINT`. Release credentials belong in the release environment.
