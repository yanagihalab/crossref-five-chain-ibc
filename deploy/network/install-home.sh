#!/bin/sh
set -eu
umask 077
root=$1
node=$2
staging=$3
mkdir -p "$root/homes"
source="$staging/$node"
target="$root/homes/$node"
test -s "$source/config/genesis.json"
test -s "$source/data/priv_validator_state.json"
if [ -e "$target" ]; then
  # Never replace signing state, consensus keys or databases on a redeploy.
  for file in config/genesis.json config/priv_validator_key.json config/node_key.json; do
    cmp -s "$source/$file" "$target/$file" || { echo "Existing home identity mismatch: $node/$file" >&2; exit 1; }
  done
  echo "Preserved existing home: $node"
else
  mv "$source" "$target"
fi
