#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-docker/docker-compose.yml}"
DENOM="${DENOM:-stake}"
CHAIN_A_ID="${CHAIN_A_ID:-crossref-a}"
CHAIN_B_ID="${CHAIN_B_ID:-crossref-b}"
CHANNEL_ID="${CHANNEL_ID:-channel-0}"

dc() {
  docker compose -f "${COMPOSE_FILE}" "$@"
}

chain_a() {
  dc exec -T chain-a crossrefd --home /var/crossref "$@"
}

chain_b() {
  dc exec -T chain-b crossrefd --home /var/crossref "$@"
}

tx_a() {
  chain_a tx crossref "$@" --from validator --chain-id "${CHAIN_A_ID}" --keyring-backend test --yes --fees "0${DENOM}"
}

tx_b() {
  chain_b tx crossref "$@" --from validator --chain-id "${CHAIN_B_ID}" --keyring-backend test --yes --fees "0${DENOM}"
}

echo "Waiting for channel ${CHANNEL_ID} on both chains..."
until chain_a query ibc channel channels --output json | grep -q "${CHANNEL_ID}"; do
  sleep 3
done
until chain_b query ibc channel channels --output json | grep -q "${CHANNEL_ID}"; do
  sleep 3
done

echo "Registering local/remote domains..."
tx_a register-domain validator chain-a "${CHAIN_A_ID}" || true
tx_a register-domain validator chain-b "${CHAIN_B_ID}" || true
tx_b register-domain validator chain-b "${CHAIN_B_ID}" || true
tx_b register-domain validator chain-a "${CHAIN_A_ID}" || true

sleep 8

echo "Binding crossref domains to ${CHANNEL_ID}..."
tx_a bind-domain-channel validator chain-a chain-b crossref "${CHANNEL_ID}" || true
tx_b bind-domain-channel validator chain-b chain-a crossref "${CHANNEL_ID}" || true

sleep 8

echo "Submitting a checkpoint on chain-a..."
tx_a submit-checkpoint validator chain-a 1 YmxvY2stMQ== YXBwLTE=

sleep 8

echo "Sending cross-reference packet chain-a -> chain-b over crossref/${CHANNEL_ID}..."
tx_a send-cross-reference-packet validator chain-a 1 crossref "${CHANNEL_ID}"

sleep 12

echo "chain-b stored cross-reference:"
chain_b query crossref cross-reference chain-b chain-a 1 --output json
