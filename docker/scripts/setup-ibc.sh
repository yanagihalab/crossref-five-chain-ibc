#!/usr/bin/env sh
set -eu

CONFIG="${HERMES_CONFIG:-/root/.hermes/config.toml}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:?RELAYER_MNEMONIC is required}"
MNEMONIC_FILE="/tmp/crossref-relayer-mnemonic"

printf '%s\n' "${RELAYER_MNEMONIC}" > "${MNEMONIC_FILE}"

echo "Waiting for crossref chains to become healthy..."
until hermes --config "${CONFIG}" health-check >/tmp/hermes-health.log 2>&1; do
  cat /tmp/hermes-health.log || true
  sleep 3
done

echo "Waiting for first blocks to settle..."
sleep "${FIRST_BLOCK_DELAY_SECONDS:-10}"

hermes --config "${CONFIG}" keys add --chain crossref-a --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true
hermes --config "${CONFIG}" keys add --chain crossref-b --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true
hermes --config "${CONFIG}" keys add --chain crossref-c --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true
hermes --config "${CONFIG}" keys add --chain crossref-d --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true
hermes --config "${CONFIG}" keys add --chain crossref-e --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" || true

create_crossref_channel() {
  a_chain="$1"
  b_chain="$2"

  echo "Creating crossref/crossref IBC channel ${a_chain} <-> ${b_chain}..."
  hermes --config "${CONFIG}" create channel \
    --a-chain "${a_chain}" \
    --b-chain "${b_chain}" \
    --a-port crossref \
    --b-port crossref \
    --new-client-connection \
    --yes || true
}

create_crossref_channel crossref-a crossref-b
create_crossref_channel crossref-a crossref-c
create_crossref_channel crossref-a crossref-d
create_crossref_channel crossref-a crossref-e
create_crossref_channel crossref-b crossref-c
create_crossref_channel crossref-b crossref-d
create_crossref_channel crossref-b crossref-e
create_crossref_channel crossref-c crossref-d
create_crossref_channel crossref-c crossref-e
create_crossref_channel crossref-d crossref-e

exec hermes --config "${CONFIG}" start
