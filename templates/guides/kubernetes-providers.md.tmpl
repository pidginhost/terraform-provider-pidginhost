---
page_title: "Using a cluster with the kubernetes and helm providers"
subcategory: ""
description: |-
  Configure the kubernetes and helm providers from the kubeconfig of a PidginHost cluster.
---

# Using a cluster with the kubernetes and helm providers

The kubeconfig of a `pidginhost_kubernetes_cluster` holds cluster admin credentials. This guide passes
them to the `kubernetes` and `helm` providers without writing a kubeconfig file.

## Keep the cluster and its workloads apart

Terraform configures providers before it creates anything, so a provider cannot use the kubeconfig of a
cluster created in the same run. Create the cluster in one configuration and deploy to it from another,
passing the cluster ID as a variable or reading it with `terraform_remote_state`.

## Terraform 1.10 and later

The `pidginhost_kubernetes_kubeconfig` ephemeral resource reads the kubeconfig on every plan and apply
and never saves it in the state.

```terraform
variable "cluster_id" {
  type = string
}

ephemeral "pidginhost_kubernetes_kubeconfig" "this" {
  cluster_id = var.cluster_id
}

locals {
  kubeconfig = yamldecode(ephemeral.pidginhost_kubernetes_kubeconfig.this.kubeconfig)
  cluster    = local.kubeconfig.clusters[0].cluster
  user       = local.kubeconfig.users[0].user
}

provider "kubernetes" {
  host                   = local.cluster.server
  cluster_ca_certificate = base64decode(local.cluster["certificate-authority-data"])
  client_certificate     = try(base64decode(local.user["client-certificate-data"]), null)
  client_key             = try(base64decode(local.user["client-key-data"]), null)
  token                  = try(local.user.token, null)
}

provider "helm" {
  kubernetes = {
    host                   = local.cluster.server
    cluster_ca_certificate = base64decode(local.cluster["certificate-authority-data"])
    client_certificate     = try(base64decode(local.user["client-certificate-data"]), null)
    client_key             = try(base64decode(local.user["client-key-data"]), null)
    token                  = try(local.user.token, null)
  }
}
```

Version 2 of the helm provider takes `kubernetes` as a block: drop the `=` after `kubernetes`.

## Older Terraform

Use the `pidginhost_kubernetes_kubeconfig` data source with the same `locals` and provider blocks,
replacing `ephemeral.pidginhost_kubernetes_kubeconfig.this` with
`data.pidginhost_kubernetes_kubeconfig.this`. The data source saves the kubeconfig in the state, so keep
the state in an encrypted backend that only cluster administrators can read.

## When the kubeconfig expires

`valid_until` says when the kubeconfig stops working. Generate a new one in the PidginHost panel. The
next plan reads it.
