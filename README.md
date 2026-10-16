# PidginHost Terraform Provider

Manage cloud servers and account SSH public keys with the PidginHost API.

- Create and import servers, resize their packages, and update project labels and login settings.
- Attach public and private networking at creation. Optionally release public IPs on server deletion.
- Apply an existing firewall set and change public interface policies.
- Enable destroy protection and manage account SSH public keys.

This version does not provide a catalog data source or separate volume, firewall, or public IP resources.
It no longer creates inline firewall sets or extra volumes. Read the [upgrade guide](docs/guides/sdk-upgrade.md)
before updating a configuration written for 0.1.x.

## Requirements

- Go 1.27.2 or newer to build, as specified in `go.mod`.
- Terraform 1.4 or newer.
- A PidginHost API token with permission to manage the selected resources.

## Usage

```hcl
terraform {
  required_providers {
    pidginhost = {
      source = "pidginhost/pidginhost"
    }
  }
}

provider "pidginhost" {}

resource "pidginhost_ssh_key" "admin" {
  alias = "admin"
  key   = file("~/.ssh/id_ed25519.pub")
}

resource "pidginhost_server" "web" {
  hostname           = "web1"
  image              = "ubuntu-24-04"
  package            = "cloudv-1"
  ssh_pub_key_id      = pidginhost_ssh_key.admin.id
  new_ipv4           = true
  destroy_public_ipv4 = true

  timeouts {
    create = "60m"
  }
}
```

Supply credentials through `PIDGINHOST_TOKEN`, or a sensitive variable passed to the provider's `token` argument.
The provider also accepts `PIDGINHOST_ACCESS_TOKEN` and `PIDGINHOST_API_TOKEN`, in that order.
Find image and package slugs through the PidginHost API or panel.

See the [provider reference](docs/index.md), [server reference](docs/resources/server.md), and
[SSH key reference](docs/resources/ssh_key.md) for all settings and import commands.

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
