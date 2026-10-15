#!/usr/bin/env sh
set -eu

CONFIG="${HERMES_CONFIG:-/root/.hermes/config.toml}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:?RELAYER_MNEMONIC is required}"
MNEMONIC_FILE="/tmp/crossref-relayer-mnemonic"
RELAYER_INDEX="${RELAYER_INDEX:-0}"
CHAIN_IDS="${CROSSREF_CHAIN_IDS:-crossref-a crossref-b crossref-c crossref-d crossref-e}"

printf '%s\n' "${RELAYER_MNEMONIC}" > "${MNEMONIC_FILE}"

RELAYER_HD_ACCOUNT_INDEX="${RELAYER_INDEX}"
if [ -z "${RELAYER_KEY_NAME:-}" ]; then
  RELAYER_KEY_NAME="relayer-worker-${RELAYER_INDEX}"
fi
if [ -z "${RELAYER_HD_PATH:-}" ]; then
  RELAYER_HD_PATH="m/44'/118'/${RELAYER_HD_ACCOUNT_INDEX}'/0/0"
fi

until hermes --config "${CONFIG}" health-check >/tmp/hermes-health.log 2>&1; do
  cat /tmp/hermes-health.log || true
  sleep 3
done

for chain_id in ${CHAIN_IDS}; do
  hermes --config "${CONFIG}" keys add --chain "${chain_id}" --key-name "${RELAYER_KEY_NAME}" --mnemonic-file "${MNEMONIC_FILE}" --hd-path "${RELAYER_HD_PATH}" --overwrite >/dev/null 2>&1 || true
done

exec hermes --config "${CONFIG}" "$@"
