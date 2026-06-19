#!/usr/bin/env bash
set -euo pipefail

CHAIN_COUNT="${CHAIN_COUNT:-5}"
RELAYER_COUNT="${RELAYER_COUNT:-3}"
FAILED_WORKER="${FAILED_WORKER:-1}"
RUN_DOCKER_FAILOVER="${RUN_DOCKER_FAILOVER:-0}"
FAILOVER_CHECKPOINT_HEIGHT="${FAILOVER_CHECKPOINT_HEIGHT:-2}"

node docker/scripts/generate-topology.mjs "${CHAIN_COUNT}" "${RELAYER_COUNT}"

stem="${CHAIN_COUNT}c-${RELAYER_COUNT}r"
topology="docker/generated/topology-${stem}.json"
compose_file="docker/generated/docker-compose-${stem}.yml"

echo "Verifying initial relayer assignment..."
node docker/scripts/verify-relayer-assignment.mjs "${topology}" "docker/generated/hermes-${stem}-worker-{worker}.toml"

echo "Rebalancing after worker ${FAILED_WORKER} failure..."
DISABLED_WORKERS="${FAILED_WORKER}" RELAYER_COUNT="${RELAYER_COUNT}" TOPOLOGY_FILE="${topology}" \
  node docker/scripts/rebalance-relayers.mjs

failover_topology="docker/generated/topology-${stem}-failover-${FAILED_WORKER}.json"
failover_override="docker/generated/docker-compose-${stem}-failover-${FAILED_WORKER}.override.yml"
node docker/scripts/verify-relayer-assignment.mjs "${failover_topology}" "docker/generated/hermes-${stem}-failover-${FAILED_WORKER}-worker-{worker}.toml"

if [ "${RUN_DOCKER_FAILOVER}" != "1" ]; then
  echo "Dry failover test passed. Set RUN_DOCKER_FAILOVER=1 to run Docker operations."
  exit 0
fi

echo "Starting topology ${stem}..."
docker compose -f "${compose_file}" up -d --build

echo "Running baseline relay experiment..."
COMPOSE_FILE="${compose_file}" TOPOLOGY_FILE="${topology}" RELAYER_WORKER_COUNT="${RELAYER_COUNT}" docker/scripts/run-crossref-experiment.sh

echo "Stopping failed worker relayer-${FAILED_WORKER}..."
docker compose -f "${compose_file}" stop "relayer-${FAILED_WORKER}"

echo "Applying failover Hermes configs to active workers..."
for worker in $(node -e 'const fs=require("fs"); const t=JSON.parse(fs.readFileSync(process.argv[1],"utf8")); console.log((t.activeRelayerWorkers||[]).join(" "))' "${failover_topology}"); do
  docker compose -f "${compose_file}" -f "${failover_override}" up -d --no-deps --force-recreate "relayer-${worker}"
done

echo "Running post-failover relay experiment at checkpoint height ${FAILOVER_CHECKPOINT_HEIGHT}..."
COMPOSE_FILE="${compose_file}" TOPOLOGY_FILE="${failover_topology}" RELAYER_WORKER_COUNT="${RELAYER_COUNT}" CHECKPOINT_HEIGHT="${FAILOVER_CHECKPOINT_HEIGHT}" SKIP_REGISTER=1 SKIP_BIND=1 docker/scripts/run-crossref-experiment.sh

cat <<EOF
Failover assignment and post-failover packet flow verified:
  topology: ${failover_topology}
  compose override: ${failover_override}
EOF
