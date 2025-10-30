#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROVIDER_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CONFIG_FILE="$(mktemp)"
cleanup() {
  rm -f "$CONFIG_FILE"
}
trap cleanup EXIT

cat >"$CONFIG_FILE" <<EOF
provider_installation {
  dev_overrides {
    "pidginhost/pidginhost" = "${PROVIDER_ROOT}/bin"
  }
  direct {
  }
}
EOF

export TF_CLI_CONFIG_FILE="$CONFIG_FILE"
cd "$SCRIPT_DIR"
terraform "$@"
