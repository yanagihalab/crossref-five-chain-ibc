#!/usr/bin/env bash
set -euo pipefail

HOME_DIR="${HOME_DIR:-/var/crossref}"
CHAIN_ID="${CHAIN_ID:?CHAIN_ID is required}"
MONIKER="${MONIKER:-validator}"
DENOM="${DENOM:-stake}"
VALIDATOR_MNEMONIC="${VALIDATOR_MNEMONIC:-}"
RELAYER_MNEMONIC="${RELAYER_MNEMONIC:-}"
RELAYER_ADDRESS="${RELAYER_ADDRESS:-}"
VALIDATOR_MNEMONIC_FILE="${VALIDATOR_MNEMONIC_FILE:-}"
RELAYER_MNEMONIC_FILE="${RELAYER_MNEMONIC_FILE:-}"
RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT:-1}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"
TIMEOUT_COMMIT="${TIMEOUT_COMMIT:-1s}"
CROSSREF_REQUIRE_CONSENSUS_PROOF="${CROSSREF_REQUIRE_CONSENSUS_PROOF:-false}"
CROSSREF_CHECKPOINT_PROOF_MAX_LAG="${CROSSREF_MAX_CHECKPOINT_PROOF_LAG:-10000}"

read_secret() {
  value="$1"
  file="$2"
  label="$3"
  if [ -n "${value}" ]; then
    printf '%s\n' "${value}"
    return 0
  fi
  if [ -n "${file}" ] && [ -f "${file}" ]; then
    IFS= read -r value <"${file}"
    if [ -n "${value}" ]; then
      printf '%s\n' "${value}"
      return 0
    fi
  fi
  echo "${label} or ${label}_FILE is required" >&2
  return 1
}

VALIDATOR_MNEMONIC="$(read_secret "${VALIDATOR_MNEMONIC}" "${VALIDATOR_MNEMONIC_FILE}" VALIDATOR_MNEMONIC)"
if [ -z "${RELAYER_MNEMONIC}" ] && [ -n "${RELAYER_MNEMONIC_FILE}" ] && [ -f "${RELAYER_MNEMONIC_FILE}" ]; then
  IFS= read -r RELAYER_MNEMONIC <"${RELAYER_MNEMONIC_FILE}"
fi
if [ -z "${RELAYER_MNEMONIC}" ] && [ -z "${RELAYER_ADDRESS}" ]; then
  echo "RELAYER_MNEMONIC, RELAYER_MNEMONIC_FILE, or RELAYER_ADDRESS is required" >&2
  exit 1
fi
if [ -z "${RELAYER_MNEMONIC}" ] && [ "${RELAYER_WORKER_COUNT}" -gt 1 ]; then
  echo "RELAYER_MNEMONIC is required when RELAYER_WORKER_COUNT is greater than 1" >&2
  exit 1
fi

if [ ! -f "${HOME_DIR}/config/genesis.json" ]; then
  crossrefd --home "${HOME_DIR}" init "${MONIKER}" --chain-id "${CHAIN_ID}"

  if ! crossrefd --home "${HOME_DIR}" keys show validator --keyring-backend "${KEYRING_BACKEND}" >/dev/null 2>&1; then
    printf '%s\n' "${VALIDATOR_MNEMONIC}" \
      | crossrefd --home "${HOME_DIR}" keys add validator --recover --keyring-backend "${KEYRING_BACKEND}"
  fi
  if [ -n "${RELAYER_MNEMONIC}" ]; then
    if ! crossrefd --home "${HOME_DIR}" keys show relayer --keyring-backend "${KEYRING_BACKEND}" >/dev/null 2>&1; then
      printf '%s\n' "${RELAYER_MNEMONIC}" \
        | crossrefd --home "${HOME_DIR}" keys add relayer --recover --keyring-backend "${KEYRING_BACKEND}"
    fi
    worker=1
    while [ "${worker}" -le "${RELAYER_WORKER_COUNT}" ]; do
      worker_key="relayer-worker-${worker}"
      worker_hd_path="m/44'/118'/${worker}'/0/0"
      if ! crossrefd --home "${HOME_DIR}" keys show "${worker_key}" --keyring-backend "${KEYRING_BACKEND}" >/dev/null 2>&1; then
        printf '%s\n' "${RELAYER_MNEMONIC}" \
          | crossrefd --home "${HOME_DIR}" keys add "${worker_key}" --recover --hd-path "${worker_hd_path}" --keyring-backend "${KEYRING_BACKEND}"
      fi
      worker=$((worker + 1))
    done
  fi

  crossrefd --home "${HOME_DIR}" genesis add-genesis-account validator "100000000000${DENOM}" --keyring-backend "${KEYRING_BACKEND}"
  if [ -n "${RELAYER_MNEMONIC}" ]; then
    crossrefd --home "${HOME_DIR}" genesis add-genesis-account relayer "100000000000${DENOM}" --keyring-backend "${KEYRING_BACKEND}"
    worker=1
    while [ "${worker}" -le "${RELAYER_WORKER_COUNT}" ]; do
      crossrefd --home "${HOME_DIR}" genesis add-genesis-account "relayer-worker-${worker}" "100000000000${DENOM}" --keyring-backend "${KEYRING_BACKEND}"
      worker=$((worker + 1))
    done
  else
    crossrefd --home "${HOME_DIR}" genesis add-genesis-account "${RELAYER_ADDRESS}" "100000000000${DENOM}"
  fi
  crossrefd --home "${HOME_DIR}" genesis gentx validator "100000000${DENOM}" --chain-id "${CHAIN_ID}" --keyring-backend "${KEYRING_BACKEND}"
  crossrefd --home "${HOME_DIR}" genesis collect-gentxs

  tmp_genesis="$(mktemp)"
  jq \
    --argjson requireConsensusProof "${CROSSREF_REQUIRE_CONSENSUS_PROOF}" \
    --argjson checkpointProofMaxLag "${CROSSREF_CHECKPOINT_PROOF_MAX_LAG}" \
    '.app_state.crossref.params.require_strict_hysteresis_signature = true
     | .app_state.crossref.params.require_consensus_proof = $requireConsensusProof
     | .app_state.crossref.params.checkpoint_proof_max_lag = $checkpointProofMaxLag
     | .app_state.crossref.params.proof_spam_window_blocks = (.app_state.crossref.params.proof_spam_window_blocks // 1000)
     | .app_state.crossref.params.proof_spam_max_failures = (.app_state.crossref.params.proof_spam_max_failures // 10)' \
    "${HOME_DIR}/config/genesis.json" >"${tmp_genesis}"
  mv "${tmp_genesis}" "${HOME_DIR}/config/genesis.json"

  crossrefd --home "${HOME_DIR}" genesis validate

  if [ -f "${HOME_DIR}/config/config.toml" ]; then
    sed -i "s/^timeout_commit = .*/timeout_commit = \"${TIMEOUT_COMMIT}\"/" "${HOME_DIR}/config/config.toml"
  fi
fi

exec crossrefd --home "${HOME_DIR}" start \
  --minimum-gas-prices "0${DENOM}" \
  --rpc.laddr tcp://0.0.0.0:26657 \
  --p2p.laddr tcp://0.0.0.0:26656 \
  --grpc.address 0.0.0.0:9090 \
  --api.enable \
  --api.address tcp://0.0.0.0:1317
