#!/usr/bin/env bash
set -euo pipefail

MATRIX="${MATRIX:-3:1 5:3 ${CHAIN_COUNT:-6}:${RELAYER_COUNT:-2}}"
RUN_DOCKER_MATRIX="${RUN_DOCKER_MATRIX:-0}"

for item in ${MATRIX}; do
  chains="${item%%:*}"
  relayers="${item##*:}"
  echo "Generating topology for ${chains} chains / ${relayers} relayers..."
  node docker/scripts/generate-topology.mjs "${chains}" "${relayers}"

  compose_file="docker/generated/docker-compose-${chains}c-${relayers}r.yml"
  topology_file="docker/generated/topology-${chains}c-${relayers}r.json"

  echo "Validating ${compose_file}..."
  docker compose -f "${compose_file}" config --quiet

  echo "Checking route distribution in ${topology_file}..."
  node -e '
    const fs = require("fs");
    const topology = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const counts = new Map();
    for (const route of topology.routes) {
      counts.set(route.relayerWorker, (counts.get(route.relayerWorker) || 0) + 1);
    }
    for (let worker = 1; worker <= topology.relayerCount; worker++) {
      if (!counts.get(worker)) throw new Error(`worker ${worker} has no routes`);
    }
    console.log(JSON.stringify({ chains: topology.chainCount, relayers: topology.relayerCount, routes: topology.routes.length, distribution: Object.fromEntries(counts) }));
  ' "${topology_file}"

  if [ "${RUN_DOCKER_MATRIX}" = "1" ]; then
    echo "Starting Docker matrix topology ${chains}c/${relayers}r..."
    docker compose -f "${compose_file}" up -d --build
    docker compose -f "${compose_file}" ps
  fi
done

echo "Crossref topology matrix test passed."
