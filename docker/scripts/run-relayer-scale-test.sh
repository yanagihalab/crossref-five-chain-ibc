#!/usr/bin/env bash
set -euo pipefail

CHAIN_COUNT="${CHAIN_COUNT:-3}"
INITIAL_RELAYER_COUNT="${INITIAL_RELAYER_COUNT:-2}"
FINAL_RELAYER_COUNT="${FINAL_RELAYER_COUNT:-4}"
BASELINE_CHECKPOINT_HEIGHT="${BASELINE_CHECKPOINT_HEIGHT:-1}"
SCALED_CHECKPOINT_HEIGHT="${SCALED_CHECKPOINT_HEIGHT:-2}"
DEMAND_FILE="${DEMAND_FILE:-}"

if [ "${INITIAL_RELAYER_COUNT}" -le 0 ] || [ "${FINAL_RELAYER_COUNT}" -le 0 ]; then
  echo "INITIAL_RELAYER_COUNT and FINAL_RELAYER_COUNT must be positive." >&2
  exit 1
fi

MAX_RELAYER_COUNT="${INITIAL_RELAYER_COUNT}"
if [ "${FINAL_RELAYER_COUNT}" -gt "${MAX_RELAYER_COUNT}" ]; then
  MAX_RELAYER_COUNT="${FINAL_RELAYER_COUNT}"
fi

stem="${CHAIN_COUNT}c-${MAX_RELAYER_COUNT}r"
base_topology="docker/generated/topology-${stem}.json"
compose_file="docker/generated/docker-compose-${stem}.yml"

node docker/scripts/generate-topology.mjs "${CHAIN_COUNT}" "${MAX_RELAYER_COUNT}"

disabled_workers_after() {
  active_count="$1"
  node -e '
    const active = Number(process.argv[1]);
    const max = Number(process.argv[2]);
    const disabled = [];
    for (let worker = active + 1; worker <= max; worker++) disabled.push(worker);
    console.log(disabled.join(" "));
  ' "${active_count}" "${MAX_RELAYER_COUNT}"
}

rebalance_topology() {
  active_count="$1"
  disabled="$(disabled_workers_after "${active_count}")"
  if [ -z "${disabled}" ]; then
    printf '%s\n' "${base_topology}"
    return 0
  fi
  if [ -n "${DEMAND_FILE}" ]; then
    DISABLED_WORKERS="${disabled}" RELAYER_COUNT="${MAX_RELAYER_COUNT}" TOPOLOGY_FILE="${base_topology}" DEMAND_FILE="${DEMAND_FILE}" \
      node docker/scripts/rebalance-relayers.mjs >/dev/null
  else
    DISABLED_WORKERS="${disabled}" RELAYER_COUNT="${MAX_RELAYER_COUNT}" TOPOLOGY_FILE="${base_topology}" \
      node docker/scripts/rebalance-relayers.mjs >/dev/null
  fi
  suffix="$(printf '%s' "${disabled}" | tr ' ' '-')"
  printf 'docker/generated/topology-%s-failover-%s.json\n' "${stem}" "${suffix}"
}

override_file_for() {
  topology_file="$1"
  suffix="${topology_file#docker/generated/topology-${stem}-}"
  suffix="${suffix%.json}"
  if [ "${topology_file}" = "${base_topology}" ]; then
    printf '%s\n' ""
  else
    printf 'docker/generated/docker-compose-%s-%s.override.yml\n' "${stem}" "${suffix}"
  fi
}

hermes_pattern_for() {
  topology_file="$1"
  if [ "${topology_file}" = "${base_topology}" ]; then
    printf 'docker/generated/hermes-%s-worker-{worker}.toml\n' "${stem}"
    return 0
  fi
  suffix="${topology_file#docker/generated/topology-${stem}-}"
  suffix="${suffix%.json}"
  printf 'docker/generated/hermes-%s-%s-worker-{worker}.toml\n' "${stem}" "${suffix}"
}

active_workers() {
  topology_file="$1"
  node -e '
    const fs = require("fs");
    const topology = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const workers = topology.activeRelayerWorkers || Array.from({ length: topology.relayerCount }, (_, index) => index + 1);
    console.log(workers.join(" "));
  ' "${topology_file}"
}

chain_services() {
  node -e '
    const fs = require("fs");
    const topology = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    console.log(topology.chains.map((chain) => chain.service).join(" "));
  ' "${base_topology}"
}

compose() {
  override="$1"
  shift
  if [ -n "${override}" ]; then
    docker compose -f "${compose_file}" -f "${override}" "$@"
  else
    docker compose -f "${compose_file}" "$@"
  fi
}

initial_topology="$(rebalance_topology "${INITIAL_RELAYER_COUNT}")"
initial_override="$(override_file_for "${initial_topology}")"
final_topology="$(rebalance_topology "${FINAL_RELAYER_COUNT}")"
final_override="$(override_file_for "${final_topology}")"

echo "Verifying initial relayer assignment (${INITIAL_RELAYER_COUNT}/${MAX_RELAYER_COUNT})..."
node docker/scripts/verify-relayer-assignment.mjs "${initial_topology}" "$(hermes_pattern_for "${initial_topology}")"
echo "Verifying final relayer assignment (${FINAL_RELAYER_COUNT}/${MAX_RELAYER_COUNT})..."
node docker/scripts/verify-relayer-assignment.mjs "${final_topology}" "$(hermes_pattern_for "${final_topology}")"

echo "Starting ${CHAIN_COUNT}-chain topology with ${INITIAL_RELAYER_COUNT} active relayer workers..."
initial_services="$(chain_services) relayer-init"
for worker in $(active_workers "${initial_topology}"); do
  initial_services="${initial_services} relayer-${worker}"
done
compose "${initial_override}" up -d --build ${initial_services}

echo "Running baseline packet flow at checkpoint height ${BASELINE_CHECKPOINT_HEIGHT}..."
COMPOSE_FILE="${compose_file}" TOPOLOGY_FILE="${initial_topology}" RELAYER_WORKER_COUNT="${MAX_RELAYER_COUNT}" CHECKPOINT_HEIGHT="${BASELINE_CHECKPOINT_HEIGHT}" \
  docker/scripts/run-crossref-experiment.sh

echo "Reassigning routes to ${FINAL_RELAYER_COUNT} active relayer workers..."
for worker in $(disabled_workers_after "${FINAL_RELAYER_COUNT}"); do
  compose "${final_override}" stop "relayer-${worker}" || true
done
for worker in $(active_workers "${final_topology}"); do
  compose "${final_override}" up -d --no-deps --force-recreate "relayer-${worker}"
done

echo "Running scaled packet flow at checkpoint height ${SCALED_CHECKPOINT_HEIGHT}..."
COMPOSE_FILE="${compose_file}" TOPOLOGY_FILE="${final_topology}" RELAYER_WORKER_COUNT="${MAX_RELAYER_COUNT}" CHECKPOINT_HEIGHT="${SCALED_CHECKPOINT_HEIGHT}" SKIP_REGISTER=1 SKIP_BIND=1 \
  docker/scripts/run-crossref-experiment.sh

echo "Relayer scale packet-flow test passed: ${INITIAL_RELAYER_COUNT} -> ${FINAL_RELAYER_COUNT} workers."
