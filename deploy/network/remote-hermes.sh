#!/bin/sh
set -eu
umask 077
CONFIG=/root/.hermes/config.toml
SECRET=/run/secrets/relayer.mnemonic
test -s "$SECRET" || { echo "Missing relayer secret" >&2; exit 1; }
for chain in ${CROSSREF_CHAIN_IDS:?}; do
  # An import error must stop the process; never silently use a different key.
  hermes --config "$CONFIG" keys add --chain "$chain" --key-name relayer \
    --mnemonic-file "$SECRET" --hd-path "m/44'/118'/0'/0/0" --overwrite >/dev/null
done
exec hermes --config "$CONFIG" "$@"
