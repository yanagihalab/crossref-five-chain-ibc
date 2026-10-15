#!/usr/bin/env bash
set -euo pipefail

MATRIX="${MATRIX:-5:10:service 5:10:one-shot 5:10:controller 3:2:controller 6:4:controller}"
RUN_DOCKER_MATRIX="${RUN_DOCKER_MATRIX:-0}"
MATRIX_KEEP_STACK="${MATRIX_KEEP_STACK:-0}"
MATRIX_CHECKPOINT_HEIGHTS="${MATRIX_CHECKPOINT_HEIGHTS:-1}"
MATRIX_SUMMARY_FILE="${MATRIX_SUMMARY_FILE:-docker/generated/crossref-matrix-summary.jsonl}"
MATRIX_CONTROLLER_QUEUE_SCOPE="${MATRIX_CONTROLLER_QUEUE_SCOPE:-account}"
MATRIX_CONTROLLER_HEALTH_MODE="${MATRIX_CONTROLLER_HEALTH_MODE:-config}"
MATRIX_SECURITY_CASES="${MATRIX_SECURITY_CASES:-}"
MATRIX_LEGACY_FAILOVER_CHECK="${MATRIX_LEGACY_FAILOVER_CHECK:-0}"

mkdir -p "$(dirname "${MATRIX_SUMMARY_FILE}")"
: >"${MATRIX_SUMMARY_FILE}"

matrix_mode_env() {
  mode="$1"
  case "${mode}" in
    service)
      printf '%s\n' \
        "CROSSREF_RELAY_AFTER_BROADCAST=service" \
        "CROSSREF_RELAYER_COMMAND_MODE=service" \
        "CROSSREF_START_RELAYER_BEFORE_BROADCAST=1"
      ;;
    one-shot)
      printf '%s\n' \
        "CROSSREF_RELAY_AFTER_BROADCAST=one-shot" \
        "CROSSREF_RELAYER_COMMAND_MODE=run" \
        "CROSSREF_START_RELAYER_BEFORE_BROADCAST=0"
      ;;
    controller)
      printf '%s\n' \
        "CROSSREF_RELAY_AFTER_BROADCAST=controller" \
        "CROSSREF_RELAYER_COMMAND_MODE=run" \
        "CROSSREF_START_RELAYER_BEFORE_BROADCAST=0" \
        "CROSSREF_CONTROLLER_QUEUE_SCOPE=${MATRIX_CONTROLLER_QUEUE_SCOPE}" \
        "CROSSREF_CONTROLLER_HEALTH_MODE=${MATRIX_CONTROLLER_HEALTH_MODE}"
      ;;
    *)
      echo "Unknown matrix mode: ${mode}" >&2
      return 1
      ;;
  esac
}

append_matrix_summary() {
  chains="$1"
  relayers="$2"
  mode="$3"
  height="$4"
  summary_file="$5"
  controller_summary_file="$6"
  mismatch_count="$7"
  node - "${MATRIX_SUMMARY_FILE}" "${chains}" "${relayers}" "${mode}" "${height}" "${summary_file}" "${controller_summary_file}" "${mismatch_count}" <<'NODE'
const fs = require("fs");
const [matrixFile, chains, relayers, mode, height, summaryFile, controllerSummaryFile, mismatchCount] = process.argv.slice(2);
const readJson = (file) => fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
const summary = readJson(summaryFile);
const controller = readJson(controllerSummaryFile);
const routeCount = Number(summary.route_count ?? controller.route_count ?? 0);
const observed = Number(summary.observed_routes ?? 0);
const row = {
  generated_at: new Date().toISOString(),
  chains: Number(chains),
  relayers: Number(relayers),
  mode,
  checkpoint_height: Number(height),
  summary_file: summaryFile,
  controller_summary_file: fs.existsSync(controllerSummaryFile) ? controllerSummaryFile : "",
  route_count: routeCount,
  delivered_routes: mode === "controller" ? Number(controller.delivered_routes ?? observed) : observed,
  expected_routes: routeCount,
  account_sequence_mismatch_count: Number(mismatchCount || 0) + Number(controller.account_sequence_mismatch_count ?? 0),
  retry_count: Number(controller.retry_count ?? 0),
  ibc_ack_event_count: Number(controller.ibc_ack_event_count ?? 0),
  crossref_ack_event_count: Number(controller.crossref_ack_event_count ?? 0),
  latency: summary.latency_from_fanout_confirmed ?? {},
  worker_utilization: controller.worker_utilization ?? {},
};
fs.appendFileSync(matrixFile, `${JSON.stringify(row)}\n`);
NODE
}

for item in ${MATRIX}; do
  IFS=: read -r chains relayers mode extra <<EOF_ITEM
${item}
EOF_ITEM
  if [ -n "${extra:-}" ]; then
    echo "Matrix item must be chains:relayers[:mode], got ${item}" >&2
    exit 1
  fi
  mode="${mode:-controller}"

  echo "Generating topology for ${chains} chains / ${relayers} relayers / ${mode} mode..."
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

  echo "Verifying worker packet filters for ${topology_file}..."
  node docker/scripts/verify-relayer-assignment.mjs "${topology_file}" "docker/generated/hermes-${chains}c-${relayers}r-worker-{worker}.toml"

  if [ "${relayers}" -gt 1 ] && [ "${MATRIX_LEGACY_FAILOVER_CHECK}" = "1" ]; then
    echo "Checking failover rebalance for worker 1..."
    FAILED_WORKER=1 CHAIN_COUNT="${chains}" RELAYER_COUNT="${relayers}" docker/scripts/run-relayer-failover-test.sh
  fi

  if [ "${mode}" = "controller" ]; then
    echo "Dry-running controller queue for ${chains}c/${relayers}r..."
    DRY_RUN=1 TOPOLOGY_FILE="${topology_file}" COMPOSE_FILE="${compose_file}" RELAYER_COUNT="${relayers}" \
      CONTROLLER_QUEUE_SCOPE="${MATRIX_CONTROLLER_QUEUE_SCOPE}" CONTROLLER_HEALTH_MODE="${MATRIX_CONTROLLER_HEALTH_MODE}" \
      CONTROLLER_METRICS_FILE="docker/generated/controller-${chains}c-${relayers}r-dryrun.jsonl" \
      CONTROLLER_SUMMARY_FILE="docker/generated/controller-${chains}c-${relayers}r-dryrun-summary.json" \
      node docker/scripts/relayer-controller.mjs --once >/dev/null
    if [ "${relayers}" -gt 1 ]; then
      echo "Dry-running controller failover for ${chains}c/${relayers}r with worker 1 disabled..."
      DRY_RUN=1 DISABLED_WORKERS=1 TOPOLOGY_FILE="${topology_file}" COMPOSE_FILE="${compose_file}" RELAYER_COUNT="${relayers}" \
        CONTROLLER_QUEUE_SCOPE="${MATRIX_CONTROLLER_QUEUE_SCOPE}" CONTROLLER_HEALTH_MODE="${MATRIX_CONTROLLER_HEALTH_MODE}" \
        CONTROLLER_METRICS_FILE="docker/generated/controller-${chains}c-${relayers}r-failover-dryrun.jsonl" \
        CONTROLLER_SUMMARY_FILE="docker/generated/controller-${chains}c-${relayers}r-failover-dryrun-summary.json" \
        node docker/scripts/relayer-controller.mjs --once >/dev/null
    fi
  fi

  if [ "${RUN_DOCKER_MATRIX}" = "1" ]; then
    for height in ${MATRIX_CHECKPOINT_HEIGHTS}; do
      run_id="matrix-${chains}c-${relayers}r-${mode}-h${height}"
      metrics_file="docker/generated/crossref-measurements-${chains}c-${relayers}r-${mode}-h${height}.jsonl"
      summary_file="docker/generated/crossref-measurements-${chains}c-${relayers}r-${mode}-h${height}.summary.json"
      controller_summary_file="${summary_file%.summary.json}.controller-summary.json"
      echo "Starting Docker matrix topology ${chains}c/${relayers}r ${mode} h${height}..."
      docker compose -f "${compose_file}" down -v --remove-orphans >/dev/null 2>&1 || true
      docker compose -f "${compose_file}" up -d --build
      docker compose -f "${compose_file}" ps
      echo "Running packet-flow experiment for ${chains}c/${relayers}r mode=${mode} height=${height}..."
      env $(matrix_mode_env "${mode}") \
        CHAIN_COUNT="${chains}" \
        RELAYER_WORKER_COUNT="${relayers}" \
        COMPOSE_FILE="${compose_file}" \
        TOPOLOGY_FILE="${topology_file}" \
        CHECKPOINT_HEIGHT="${height}" \
        CROSSREF_RUN_ID="${run_id}" \
        CROSSREF_METRICS_FILE="${metrics_file}" \
        CROSSREF_METRICS_SUMMARY_FILE="${summary_file}" \
        docker/scripts/run-crossref-experiment.sh

      mismatch_count="$(docker compose -f "${compose_file}" logs 2>/dev/null | grep -c 'account sequence mismatch' || true)"
      append_matrix_summary "${chains}" "${relayers}" "${mode}" "${height}" "${summary_file}" "${controller_summary_file}" "${mismatch_count}"

      if [ -n "${MATRIX_SECURITY_CASES}" ]; then
        echo "Running security hardening cases for ${chains}c/${relayers}r ${mode}: ${MATRIX_SECURITY_CASES}"
        SECURITY_CASES="${MATRIX_SECURITY_CASES}" CHAIN_COUNT="${chains}" RELAYER_WORKER_COUNT="${relayers}" COMPOSE_FILE="${compose_file}" TOPOLOGY_FILE="${topology_file}" \
          docker/scripts/run-security-hardening-test.sh
      fi

      if [ "${MATRIX_KEEP_STACK}" != "1" ]; then
        docker compose -f "${compose_file}" down -v --remove-orphans
      fi
    done
  fi
done

echo "Crossref topology matrix test passed."
