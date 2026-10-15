#!/usr/bin/env bash
set -euo pipefail

ITERATIONS="${ITERATIONS:-50}"
START_HEIGHT="${START_HEIGHT:-2}"
CHAIN_COUNT="${CHAIN_COUNT:-5}"
RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT:-1}"
COMPOSE_FILE="${COMPOSE_FILE:-docker/docker-compose.yml}"
RELAYER_SERVICE="${RELAYER_SERVICE:-relayer}"
TOPOLOGY_FILE="${TOPOLOGY_FILE:-docker/generated/topology-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r.json}"
BENCHMARK_DIR="${BENCHMARK_DIR:-docker/generated/benchmark-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r-$(date +%Y%m%d%H%M%S)}"
CROSSREF_BROADCAST_MODE="${CROSSREF_BROADCAST_MODE:-parallel}"
CROSSREF_VERIFY_SLEEP_SECONDS="${CROSSREF_VERIFY_SLEEP_SECONDS:-1}"
CROSSREF_VERIFY_RETRIES="${CROSSREF_VERIFY_RETRIES:-36}"
CROSSREF_RELAY_SETTLE_SECONDS="${CROSSREF_RELAY_SETTLE_SECONDS:-0}"
CROSSREF_ISOLATE_RELAYER_FOR_UPDATES="${CROSSREF_ISOLATE_RELAYER_FOR_UPDATES:-1}"

mkdir -p "${BENCHMARK_DIR}/logs" "${BENCHMARK_DIR}/metrics"

echo "Running crossref benchmark:"
echo "  iterations: ${ITERATIONS}"
echo "  start_height: ${START_HEIGHT}"
echo "  chain_count: ${CHAIN_COUNT}"
echo "  relayer_worker_count: ${RELAYER_WORKER_COUNT}"
echo "  broadcast_mode: ${CROSSREF_BROADCAST_MODE}"
echo "  benchmark_dir: ${BENCHMARK_DIR}"

for iteration in $(seq 1 "${ITERATIONS}"); do
  height=$((START_HEIGHT + iteration - 1))
  metrics_file="${BENCHMARK_DIR}/metrics/iteration-${iteration}-h${height}.jsonl"
  summary_file="${BENCHMARK_DIR}/metrics/iteration-${iteration}-h${height}.summary.json"
  log_file="${BENCHMARK_DIR}/logs/iteration-${iteration}-h${height}.log"

  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] iteration ${iteration}/${ITERATIONS}, checkpoint height ${height}"
  relayer_command_mode="service"
  start_relayer_before_broadcast="0"
  if [ "${CROSSREF_ISOLATE_RELAYER_FOR_UPDATES}" = "1" ]; then
    docker compose -f "${COMPOSE_FILE}" stop "${RELAYER_SERVICE}" >/dev/null 2>&1 || true
    relayer_command_mode="run"
    start_relayer_before_broadcast="1"
  fi
  if ! CHAIN_COUNT="${CHAIN_COUNT}" \
    RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT}" \
    RELAYER_SERVICE="${RELAYER_SERVICE}" \
    COMPOSE_FILE="${COMPOSE_FILE}" \
    TOPOLOGY_FILE="${TOPOLOGY_FILE}" \
    CHECKPOINT_HEIGHT="${height}" \
    SKIP_REGISTER=1 \
    SKIP_BIND=1 \
    CROSSREF_MEASUREMENTS=1 \
    CROSSREF_METRICS_FILE="${metrics_file}" \
    CROSSREF_METRICS_SUMMARY_FILE="${summary_file}" \
    CROSSREF_BROADCAST_MODE="${CROSSREF_BROADCAST_MODE}" \
    CROSSREF_VERIFY_SLEEP_SECONDS="${CROSSREF_VERIFY_SLEEP_SECONDS}" \
    CROSSREF_VERIFY_RETRIES="${CROSSREF_VERIFY_RETRIES}" \
    CROSSREF_RELAY_SETTLE_SECONDS="${CROSSREF_RELAY_SETTLE_SECONDS}" \
    CROSSREF_RELAYER_COMMAND_MODE="${relayer_command_mode}" \
    CROSSREF_START_RELAYER_BEFORE_BROADCAST="${start_relayer_before_broadcast}" \
    docker/scripts/run-crossref-experiment.sh >"${log_file}" 2>&1; then
    echo "Benchmark failed at iteration ${iteration}, height ${height}. See ${log_file}" >&2
    exit 1
  fi
done

node - "${BENCHMARK_DIR}" <<'NODE'
const fs = require("fs");
const path = require("path");

const benchmarkDir = process.argv[2];
const metricsDir = path.join(benchmarkDir, "metrics");
const summaries = fs.readdirSync(metricsDir)
  .filter((name) => name.endsWith(".summary.json"))
  .sort((a, b) => {
    const ai = Number((a.match(/iteration-(\d+)-/) || [])[1] || 0);
    const bi = Number((b.match(/iteration-(\d+)-/) || [])[1] || 0);
    return ai - bi;
  })
  .map((name) => JSON.parse(fs.readFileSync(path.join(metricsDir, name), "utf8")));

const routeMeasurements = summaries.flatMap((summary) => summary.measurements || []);
const confirmedLatencies = routeMeasurements
  .map((item) => item.latency_from_fanout_confirmed_ms)
  .filter(Number.isFinite);
const startLatencies = routeMeasurements
  .map((item) => item.latency_from_fanout_start_ms)
  .filter(Number.isFinite);

function stats(values) {
  const sorted = [...values].sort((a, b) => a - b);
  if (sorted.length === 0) {
    return { count: 0, min_ms: null, p50_ms: null, avg_ms: null, p95_ms: null, max_ms: null };
  }
  const quantile = (q) => sorted[Math.min(sorted.length - 1, Math.floor((sorted.length - 1) * q))];
  return {
    count: sorted.length,
    min_ms: sorted[0],
    p50_ms: quantile(0.5),
    avg_ms: Math.round(sorted.reduce((sum, value) => sum + value, 0) / sorted.length),
    p95_ms: quantile(0.95),
    max_ms: sorted[sorted.length - 1],
  };
}

const byRoute = new Map();
for (const item of routeMeasurements) {
  const key = item.route_id || `${item.source_domain}->${item.destination_domain}`;
  if (!byRoute.has(key)) byRoute.set(key, []);
  if (Number.isFinite(item.latency_from_fanout_confirmed_ms)) {
    byRoute.get(key).push(item.latency_from_fanout_confirmed_ms);
  }
}

const result = {
  benchmark_dir: benchmarkDir,
  iterations: summaries.length,
  route_samples: routeMeasurements.length,
  latency_from_fanout_confirmed: stats(confirmedLatencies),
  latency_from_fanout_start: stats(startLatencies),
  routes: [...byRoute.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([route_id, values]) => ({
    route_id,
    ...stats(values),
  })),
};

const outFile = path.join(benchmarkDir, "benchmark-summary.json");
fs.writeFileSync(outFile, `${JSON.stringify(result, null, 2)}\n`);
console.log(JSON.stringify(result, null, 2));
NODE
