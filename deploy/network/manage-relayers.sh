#!/usr/bin/env bash
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
NODE_BIN="${NODE_BIN:-node}"
if ! command -v "$NODE_BIN" >/dev/null 2>&1; then
  printf 'Node.js is required (or set NODE_BIN).\n' >&2
  exit 1
fi
exec "$NODE_BIN" "$SCRIPT_DIR/manage-relayers.mjs" "$@"
