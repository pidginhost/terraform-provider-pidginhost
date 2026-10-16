# Look a key up by exactly one of id, alias or fingerprint.
data "pidginhost_ssh_key" "laptop" {
  alias = "laptop"
}

output "laptop_key_id" {
  value = data.pidginhost_ssh_key.laptop.id
}
