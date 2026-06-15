#!/usr/bin/env sh
set -eu

CONFIG="${HERMES_CONFIG:-/root/.hermes/config.toml}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:?RELAYER_MNEMONIC is required}"
MNEMONIC_FILE="/tmp/crossref-relayer-mnemonic"

printf '%s\n' "${RELAYER_MNEMONIC}" > "${MNEMONIC_FILE}"

echo "Waiting for both crossref chains to become healthy..."
until hermes --config "${CONFIG}" health-check >/tmp/hermes-health.log 2>&1; do
  cat /tmp/hermes-health.log || true
  sleep 3
done

hermes --config "${CONFIG}" keys add --chain crossref-a --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true
hermes --config "${CONFIG}" keys add --chain crossref-b --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true

echo "Creating crossref/crossref IBC channel..."
hermes --config "${CONFIG}" create channel \
  --a-chain crossref-a \
  --b-chain crossref-b \
  --a-port crossref \
  --b-port crossref \
  --new-client-connection \
  --yes || true

exec hermes --config "${CONFIG}" start
