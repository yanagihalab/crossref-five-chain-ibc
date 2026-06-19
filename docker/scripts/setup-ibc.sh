#!/usr/bin/env sh
set -eu

CONFIG="${HERMES_CONFIG:-/root/.hermes/config.toml}"
RELAYER_MODE="${RELAYER_MODE:-init-and-start}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:?RELAYER_MNEMONIC is required}"
MNEMONIC_FILE="/tmp/crossref-relayer-mnemonic"
RELAYER_INDEX="${RELAYER_INDEX:-0}"
CHAIN_IDS="${CROSSREF_CHAIN_IDS:-crossref-a crossref-b crossref-c crossref-d crossref-e}"
CHANNEL_PAIRS="${CROSSREF_CHANNEL_PAIRS:-crossref-a:crossref-b crossref-a:crossref-c crossref-a:crossref-d crossref-a:crossref-e crossref-b:crossref-c crossref-b:crossref-d crossref-b:crossref-e crossref-c:crossref-d crossref-c:crossref-e crossref-d:crossref-e}"

printf '%s\n' "${RELAYER_MNEMONIC}" > "${MNEMONIC_FILE}"

if [ "${RELAYER_MODE}" = "start" ]; then
  RELAYER_HD_ACCOUNT_INDEX="${RELAYER_INDEX}"
else
  RELAYER_HD_ACCOUNT_INDEX="${RELAYER_HD_ACCOUNT_INDEX:-0}"
fi
RELAYER_HD_PATH="${RELAYER_HD_PATH:-m/44'/118'/${RELAYER_HD_ACCOUNT_INDEX}'/0/0}"

echo "Waiting for crossref chains to become healthy..."
until hermes --config "${CONFIG}" health-check >/tmp/hermes-health.log 2>&1; do
  cat /tmp/hermes-health.log || true
  sleep 3
done

echo "Waiting for first blocks to settle..."
sleep "${FIRST_BLOCK_DELAY_SECONDS:-10}"

for chain_id in ${CHAIN_IDS}; do
  hermes --config "${CONFIG}" keys add --chain "${chain_id}" --key-name relayer --mnemonic-file "${MNEMONIC_FILE}" --hd-path "${RELAYER_HD_PATH}" --overwrite || true
done

if [ "${RELAYER_MODE}" = "start" ]; then
  echo "Starting Hermes relayer worker ${RELAYER_INDEX} with HD path ${RELAYER_HD_PATH}..."
  exec hermes --config "${CONFIG}" start
fi

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

for pair in ${CHANNEL_PAIRS}; do
  create_crossref_channel "${pair%%:*}" "${pair##*:}"
done

if [ "${RELAYER_MODE}" = "init" ]; then
  echo "IBC channel initialization complete."
  exit 0
fi

exec hermes --config "${CONFIG}" start
