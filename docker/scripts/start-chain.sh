#!/usr/bin/env bash
set -euo pipefail

HOME_DIR="${HOME_DIR:-/var/crossref}"
CHAIN_ID="${CHAIN_ID:?CHAIN_ID is required}"
MONIKER="${MONIKER:-validator}"
DENOM="${DENOM:-stake}"
VALIDATOR_MNEMONIC="${VALIDATOR_MNEMONIC:?VALIDATOR_MNEMONIC is required}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:?RELAYER_MNEMONIC is required}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"

if [ ! -f "${HOME_DIR}/config/genesis.json" ]; then
  crossrefd --home "${HOME_DIR}" init "${MONIKER}" --chain-id "${CHAIN_ID}"

  if ! crossrefd --home "${HOME_DIR}" keys show validator --keyring-backend "${KEYRING_BACKEND}" >/dev/null 2>&1; then
    printf '%s\n' "${VALIDATOR_MNEMONIC}" \
      | crossrefd --home "${HOME_DIR}" keys add validator --recover --keyring-backend "${KEYRING_BACKEND}"
  fi
  if ! crossrefd --home "${HOME_DIR}" keys show relayer --keyring-backend "${KEYRING_BACKEND}" >/dev/null 2>&1; then
    printf '%s\n' "${RELAYER_MNEMONIC}" \
      | crossrefd --home "${HOME_DIR}" keys add relayer --recover --keyring-backend "${KEYRING_BACKEND}"
  fi

  crossrefd --home "${HOME_DIR}" genesis add-genesis-account validator "100000000000${DENOM}" --keyring-backend "${KEYRING_BACKEND}"
  crossrefd --home "${HOME_DIR}" genesis add-genesis-account relayer "100000000000${DENOM}" --keyring-backend "${KEYRING_BACKEND}"
  crossrefd --home "${HOME_DIR}" genesis gentx validator "100000000${DENOM}" --chain-id "${CHAIN_ID}" --keyring-backend "${KEYRING_BACKEND}"
  crossrefd --home "${HOME_DIR}" genesis collect-gentxs
  crossrefd --home "${HOME_DIR}" genesis validate
fi

exec crossrefd --home "${HOME_DIR}" start \
  --minimum-gas-prices "0${DENOM}" \
  --rpc.laddr tcp://0.0.0.0:26657 \
  --p2p.laddr tcp://0.0.0.0:26656 \
  --grpc.address 0.0.0.0:9090 \
  --api.enable \
  --api.address tcp://0.0.0.0:1317
