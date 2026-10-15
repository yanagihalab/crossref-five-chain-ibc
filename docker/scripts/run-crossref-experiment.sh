#!/usr/bin/env bash
set -euo pipefail

CHAIN_COUNT="${CHAIN_COUNT:-5}"
RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT:-1}"
TOPOLOGY_FILE="${TOPOLOGY_FILE:-docker/generated/topology-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r.json}"
COMPOSE_FILE="${COMPOSE_FILE:-docker/docker-compose.yml}"
RELAYER_SERVICE="${RELAYER_SERVICE:-relayer}"
RELAYER_INDEX="${RELAYER_INDEX:-1}"
DENOM="${DENOM:-stake}"
CROSSREF_TX_GAS="${CROSSREF_TX_GAS:-600000}"
BLOCK_TIME_UNIX="${BLOCK_TIME_UNIX:-0}"
CHECKPOINT_HEIGHT="${CHECKPOINT_HEIGHT:-1}"
CROSSREF_USE_REAL_BLOCK_HASHES="${CROSSREF_USE_REAL_BLOCK_HASHES:-1}"
CROSSREF_CONSENSUS_PROOF_MODE="${CROSSREF_CONSENSUS_PROOF_MODE:-digest}"
DRY_RUN="${DRY_RUN:-0}"
SKIP_REGISTER="${SKIP_REGISTER:-0}"
SKIP_BIND="${SKIP_BIND:-0}"
SKIP_CHECKPOINT_SUBMIT="${SKIP_CHECKPOINT_SUBMIT:-0}"
CROSSREF_VERIFY_RETRIES="${CROSSREF_VERIFY_RETRIES:-36}"
CROSSREF_VERIFY_SLEEP_SECONDS="${CROSSREF_VERIFY_SLEEP_SECONDS:-5}"
CROSSREF_BROADCAST_MODE="${CROSSREF_BROADCAST_MODE:-sequential}"
CROSSREF_MEASUREMENTS="${CROSSREF_MEASUREMENTS:-1}"
CROSSREF_RELAY_SETTLE_SECONDS="${CROSSREF_RELAY_SETTLE_SECONDS:-20}"
CROSSREF_RELAYER_COMMAND_MODE="${CROSSREF_RELAYER_COMMAND_MODE:-service}"
CROSSREF_START_RELAYER_BEFORE_BROADCAST="${CROSSREF_START_RELAYER_BEFORE_BROADCAST:-0}"
CROSSREF_RELAY_AFTER_BROADCAST="${CROSSREF_RELAY_AFTER_BROADCAST:-service}"
CROSSREF_CONTROLLER_QUEUE_SCOPE="${CROSSREF_CONTROLLER_QUEUE_SCOPE:-account}"
CROSSREF_CONTROLLER_HEALTH_MODE="${CROSSREF_CONTROLLER_HEALTH_MODE:-config}"
CROSSREF_PHASE="${CROSSREF_PHASE:-all}"
case "${CROSSREF_PHASE}" in all|prepare|deliver) ;; *) echo "Invalid CROSSREF_PHASE" >&2; exit 1 ;; esac
if [ "${CROSSREF_PHASE}" = "prepare" ] && [ "${CROSSREF_RELAYER_COMMAND_MODE}" != "run" ]; then
  echo "prepare requires one-shot commands with the service stopped by its coordinator" >&2
  exit 1
fi

if [ ! -f "${TOPOLOGY_FILE}" ]; then
  echo "Topology ${TOPOLOGY_FILE} not found; generating ${CHAIN_COUNT} chains / ${RELAYER_WORKER_COUNT} relayers..."
  node docker/scripts/generate-topology.mjs "${CHAIN_COUNT}" "${RELAYER_WORKER_COUNT}" >/dev/null
fi

if [ ! -f "${TOPOLOGY_FILE}" ]; then
  echo "Topology file not found after generation: ${TOPOLOGY_FILE}" >&2
  exit 1
fi

dc() {
  if [ -n "${CROSSREF_COMPOSE_BRIDGE:-}" ]; then
    node "${CROSSREF_COMPOSE_BRIDGE}" "$@"
  else
    docker compose -f "${COMPOSE_FILE}" "$@"
  fi
}

topology_query() {
  node -e '
    const fs = require("fs");
    const topology = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const query = process.argv[2];
    const a = process.argv[3];
    const b = process.argv[4];
    const route = (from, to) => topology.routes.find((r) => r.from === from && r.to === to);
    const chain = (domain) => topology.chains.find((c) => c.domain === domain);
    if (query === "domains") console.log(topology.chains.map((c) => c.domain).join(" "));
    else if (query === "pairs") console.log(topology.pairs.map((p) => `${p.left}:${p.right}`).join(" "));
    else if (query === "routes") console.log(topology.routes.map((r) => `${r.from}:${r.to}`).join(" "));
    else if (query === "chain-id") console.log(chain(a)?.chainId || "");
    else if (query === "service") console.log(chain(a)?.service || a);
    else if (query === "rpc-port") console.log(chain(a)?.rpcPort || "");
    else if (query === "rpc-url") console.log(chain(a)?.rpcUrl || `http://localhost:${chain(a)?.rpcPort}`);
    else if (query === "route-id") console.log(route(a, b)?.id || "");
    else if (query === "channel") console.log(route(a, b)?.fromChannel || "");
    else if (query === "counterparty-channel") console.log(route(a, b)?.toChannel || "");
    else if (query === "worker") console.log(route(a, b)?.relayerWorker || 1);
    else if (query === "route-count") console.log(topology.routes.length);
    else if (query === "chain-count") console.log(topology.chains.length);
    else {
      console.error(`unknown topology query: ${query}`);
      process.exit(2);
    }
  ' "${TOPOLOGY_FILE}" "$@"
}

DOMAINS="$(topology_query domains)"
PAIRS="$(topology_query pairs)"
ROUTES="$(topology_query routes)"
ROUTE_COUNT="$(topology_query route-count)"
ACTUAL_CHAIN_COUNT="$(topology_query chain-count)"
CROSSREF_RUN_ID="${CROSSREF_RUN_ID:-crossref-${ACTUAL_CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r-h${CHECKPOINT_HEIGHT}-$(node -e 'process.stdout.write(String(Date.now()))')}"
CROSSREF_METRICS_FILE="${CROSSREF_METRICS_FILE:-docker/generated/crossref-measurements-${ACTUAL_CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r-h${CHECKPOINT_HEIGHT}.jsonl}"
CROSSREF_METRICS_SUMMARY_FILE="${CROSSREF_METRICS_SUMMARY_FILE:-docker/generated/crossref-measurements-${ACTUAL_CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r-h${CHECKPOINT_HEIGHT}.summary.json}"

now_ms() {
  node -e 'process.stdout.write(String(Date.now()))'
}

metric_event() {
  if [ "${CROSSREF_MEASUREMENTS}" != "1" ]; then
    return 0
  fi
  event_type="$1"
  shift
  event_time_ms="$(now_ms)"
  node - "${CROSSREF_METRICS_FILE}" "${CROSSREF_RUN_ID}" "${event_type}" "${event_time_ms}" "$@" <<'NODE'
const fs = require("fs");
const [file, runId, type, timeMs, ...pairs] = process.argv.slice(2);
const event = { run_id: runId, type, time_ms: Number(timeMs) };
for (let i = 0; i < pairs.length; i += 2) {
  const key = pairs[i];
  const value = pairs[i + 1] ?? "";
  event[key] = /^-?\d+$/.test(value) ? Number(value) : value;
}
fs.appendFileSync(file, `${JSON.stringify(event)}\n`);
NODE
}

init_measurements() {
  if [ "${CROSSREF_MEASUREMENTS}" != "1" ]; then
    return 0
  fi
  mkdir -p "$(dirname "${CROSSREF_METRICS_FILE}")" "$(dirname "${CROSSREF_METRICS_SUMMARY_FILE}")"
  : >"${CROSSREF_METRICS_FILE}"
  metric_event experiment_start \
    topology_file "${TOPOLOGY_FILE}" \
    compose_file "${COMPOSE_FILE}" \
    chain_count "${ACTUAL_CHAIN_COUNT}" \
    relayer_worker_count "${RELAYER_WORKER_COUNT}" \
    route_count "${ROUTE_COUNT}" \
    checkpoint_height "${CHECKPOINT_HEIGHT}" \
    broadcast_mode "${CROSSREF_BROADCAST_MODE}" \
    relayer_command_mode "${CROSSREF_RELAYER_COMMAND_MODE}" \
    relay_after_broadcast "${CROSSREF_RELAY_AFTER_BROADCAST}" \
    controller_queue_scope "${CROSSREF_CONTROLLER_QUEUE_SCOPE}" \
    verify_sleep_seconds "${CROSSREF_VERIFY_SLEEP_SECONDS}"
}

finalize_measurements() {
  if [ "${CROSSREF_MEASUREMENTS}" != "1" ]; then
    return 0
  fi
  metric_event experiment_end
  node - "${CROSSREF_METRICS_FILE}" "${CROSSREF_METRICS_SUMMARY_FILE}" "${TOPOLOGY_FILE}" <<'NODE'
const fs = require("fs");
const [eventsFile, summaryFile, topologyFile] = process.argv.slice(2);
const events = fs.readFileSync(eventsFile, "utf8").trim().split(/\n+/).filter(Boolean).map((line) => JSON.parse(line));
const topology = JSON.parse(fs.readFileSync(topologyFile, "utf8"));
const fanoutStart = new Map();
const fanoutConfirmed = new Map();
for (const event of events) {
  if (event.type === "fanout_start") fanoutStart.set(event.source_domain, event.time_ms);
  if (event.type === "fanout_confirmed") fanoutConfirmed.set(event.source_domain, event.time_ms);
}

const routeByPair = new Map(topology.routes.map((route) => [`${route.from}->${route.to}`, route]));
const measurements = events
  .filter((event) => event.type === "reference_visible")
  .map((event) => {
    const source = event.remote_domain;
    const destination = event.local_domain;
    const route = routeByPair.get(`${source}->${destination}`) || {};
    const start = fanoutStart.get(source);
    const confirmed = fanoutConfirmed.get(source);
    return {
      route_id: event.route_id,
      source_domain: source,
      destination_domain: destination,
      source_channel: route.fromChannel || "",
      destination_channel: route.toChannel || "",
      relayer_worker: route.relayerWorker || event.relayer_worker || 1,
      visible_attempt: event.attempt,
      visible_time_ms: event.time_ms,
      fanout_start_time_ms: start ?? null,
      fanout_confirmed_time_ms: confirmed ?? null,
      latency_from_fanout_start_ms: Number.isFinite(start) ? event.time_ms - start : null,
      latency_from_fanout_confirmed_ms: Number.isFinite(confirmed) ? event.time_ms - confirmed : null,
    };
  });

function stats(values) {
  const sorted = values.filter(Number.isFinite).sort((a, b) => a - b);
  if (sorted.length === 0) return { count: 0, min_ms: null, p50_ms: null, avg_ms: null, p95_ms: null, max_ms: null };
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

const summary = {
  generated_from: eventsFile,
  topology_file: topologyFile,
  run_id: events[0]?.run_id || "",
  chain_count: topology.chains.length,
  route_count: topology.routes.length,
  observed_routes: measurements.length,
  latency_from_fanout_start: stats(measurements.map((item) => item.latency_from_fanout_start_ms)),
  latency_from_fanout_confirmed: stats(measurements.map((item) => item.latency_from_fanout_confirmed_ms)),
  measurements,
  events,
};

fs.writeFileSync(summaryFile, `${JSON.stringify(summary, null, 2)}\n`);
console.log(JSON.stringify({
  measurements: summary.observed_routes,
  summary: summaryFile,
  latency_from_fanout_confirmed: summary.latency_from_fanout_confirmed,
}, null, 2));
NODE
}

chain_id() {
  topology_query chain-id "$1"
}

service_for_domain() {
  topology_query service "$1"
}

rpc_port_for_domain() {
  topology_query rpc-port "$1"
}

channel_id() {
  topology_query channel "$1" "$2"
}

counterparty_channel_id() {
  topology_query counterparty-channel "$1" "$2"
}

route_id() {
  topology_query route-id "$1" "$2"
}

route_worker() {
  topology_query worker "$1" "$2"
}

client_for_source() {
  local host_domain="$1" source_domain="$2" host_channel channel_json connection_id connection_json
  host_channel="$(channel_id "${host_domain}" "${source_domain}")" || return 1
  channel_json="$(query_chain "${host_domain}" query ibc channel end crossref "${host_channel}" --output json)" || return 1
  # Client, connection, and channel counters diverge after interrupted handshakes.
  connection_id="$(printf '%s' "${channel_json}" | node -e '
    const value = JSON.parse(require("fs").readFileSync(0, "utf8")).channel;
    if (value?.state !== "STATE_OPEN" || value.connection_hops?.length !== 1 || !value.connection_hops[0]) {
      console.error("Expected an OPEN channel with one connection hop");
      process.exit(1);
    }
    console.log(value.connection_hops[0]);
  ')" || return 1
  connection_json="$(query_chain "${host_domain}" query ibc connection end "${connection_id}" --output json)" || return 1
  printf '%s' "${connection_json}" | node -e '
    const value = JSON.parse(require("fs").readFileSync(0, "utf8")).connection;
    if (value?.state !== "STATE_OPEN" || !value.client_id) {
      console.error("Expected an OPEN connection with a client ID");
      process.exit(1);
    }
    console.log(value.client_id);
  '
}

query_chain() {
  domain="$1"
  shift
  dc exec -T "$(service_for_domain "${domain}")" crossrefd --home /var/crossref "$@"
}

relayer_service_exists() {
  dc config --services | grep -qx "$1"
}

relayer_for_route() {
  source_domain="$1"
  host_domain="$2"
  worker="$(route_worker "${source_domain}" "${host_domain}" | tr -cd '0-9')"
  if [ -z "${worker}" ]; then
    echo "Could not resolve relayer worker for ${source_domain}->${host_domain}." >&2
    return 1
  fi
  if [ "${CROSSREF_RELAYER_COMMAND_MODE}" = "run" ]; then
    if [ -n "${CROSSREF_COMPOSE_BRIDGE:-}" ] && [ "${RELAYER_WORKER_COUNT}" = "1" ]; then
      dc run --rm --no-deps "${RELAYER_SERVICE}" "${@:3}"
    elif [ "${RELAYER_WORKER_COUNT}" -gt 1 ]; then
      dc run --rm --no-deps --entrypoint /bin/sh "relayer-${worker}" /opt/crossref/scripts/hermes-one-shot.sh "${@:3}"
    elif relayer_service_exists "${RELAYER_SERVICE}"; then
      dc run --rm --no-deps --entrypoint /bin/sh "${RELAYER_SERVICE}" /opt/crossref/scripts/hermes-one-shot.sh "${@:3}"
    else
      echo "Relayer service ${RELAYER_SERVICE} is not available for one-shot command mode." >&2
      return 1
    fi
  elif [ "${RELAYER_WORKER_COUNT}" -gt 1 ]; then
    dc exec -T "relayer-${worker}" hermes "${@:3}"
  elif [ "${RELAYER_WORKER_COUNT}" -le 1 ] && relayer_service_exists "${RELAYER_SERVICE}"; then
    dc exec -T --index "${worker}" "${RELAYER_SERVICE}" hermes "${@:3}"
  else
    echo "Relayer worker service relayer-${worker} is not available for ${source_domain}->${host_domain}." >&2
    return 1
  fi
}

start_relayer_services() {
  if [ "${RELAYER_WORKER_COUNT}" -gt 1 ]; then
    for worker in $(seq 1 "${RELAYER_WORKER_COUNT}"); do
      dc up -d --no-deps "relayer-${worker}"
    done
  elif relayer_service_exists "${RELAYER_SERVICE}"; then
    dc up -d --no-deps "${RELAYER_SERVICE}"
  fi
}

stop_relayer_services() {
  if [ "${RELAYER_WORKER_COUNT}" -gt 1 ]; then
    for worker in $(seq 1 "${RELAYER_WORKER_COUNT}"); do
      dc stop "relayer-${worker}" >/dev/null 2>&1 || true
    done
  elif relayer_service_exists "${RELAYER_SERVICE}"; then
    dc stop "${RELAYER_SERVICE}" >/dev/null 2>&1 || true
  fi
}

relay_packets_once() {
  source_domain="$1"
  host_domain="$2"
  relayer_for_route "${source_domain}" "${host_domain}" clear packets \
    --chain "$(chain_id "${source_domain}")" \
    --port crossref \
    --channel "$(channel_id "${source_domain}" "${host_domain}")"
}

relay_all_routes_once() {
  echo "Relaying packets route-by-route with one-shot Hermes commands..."
  for route in ${ROUTES}; do
    source_domain="${route%%:*}"
    host_domain="${route#*:}"
    echo "One-shot relay ${source_domain} -> ${host_domain} via ${source_domain}/$(channel_id "${source_domain}" "${host_domain}")..."
    relay_packets_once "${source_domain}" "${host_domain}"
  done
}

relay_all_routes_with_controller() {
  echo "Relaying packets with route queue controller..."
  CONTROLLER_ONCE=1 \
    CONTROLLER_QUEUE_SCOPE="${CROSSREF_CONTROLLER_QUEUE_SCOPE}" \
    CONTROLLER_HEALTH_MODE="${CROSSREF_CONTROLLER_HEALTH_MODE}" \
    CONTROLLER_METRICS_FILE="${CROSSREF_METRICS_FILE}" \
    CONTROLLER_SUMMARY_FILE="${CROSSREF_METRICS_SUMMARY_FILE%.summary.json}.controller-summary.json" \
    TOPOLOGY_FILE="${TOPOLOGY_FILE}" \
    COMPOSE_FILE="${COMPOSE_FILE}" \
    RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT}" \
    RELAYER_COUNT="${RELAYER_WORKER_COUNT}" \
    CROSSREF_RUN_ID="${CROSSREF_RUN_ID}" \
    node docker/scripts/relayer-controller.mjs --once
}

tx_chain() {
  domain="$1"
  shift
  query_chain "${domain}" tx crossref "$@" --from validator --chain-id "$(chain_id "${domain}")" --keyring-backend test --yes --gas "${CROSSREF_TX_GAS}" --fees "0${DENOM}"
}

wait_tx() {
  domain="$1"
  txhash="$2"
  attempt=1

  while [ "${attempt}" -le 30 ]; do
    if out="$(query_chain "${domain}" query tx "${txhash}" --output json 2>&1)"; then
      printf '%s\n' "${out}"
      if printf '%s\n' "${out}" | grep -Eq '"code"[[:space:]]*:[[:space:]]*"?0"?'; then
        return 0
      fi
      echo "Transaction ${txhash} on ${domain} was included with a non-zero code." >&2
      return 1
    fi
    sleep 2
    attempt=$((attempt + 1))
  done

  echo "Timed out waiting for transaction ${txhash} on ${domain}." >&2
  return 1
}

run_tx() {
  domain="$1"
  shift
  tx_command_name="${1:-unknown}"
  tx_start_ms="$(now_ms)"
  out="$(tx_chain "${domain}" "$@" --output json 2>&1)"
  tx_response_ms="$(now_ms)"
  printf '%s\n' "${out}"
  txhash="$(printf '%s\n' "${out}" | sed -n 's/.*"txhash"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | tail -1)"
  if [ -z "${txhash}" ]; then
    metric_event txhash_parse_failed \
      domain "${domain}" \
      command "${tx_command_name}" \
      response_latency_ms "$((tx_response_ms - tx_start_ms))"
    echo "Could not parse ${domain} txhash." >&2
    return 1
  fi
  metric_event tx_broadcast_response \
    domain "${domain}" \
    txhash "${txhash}" \
    command "${tx_command_name}" \
    start_time_ms "${tx_start_ms}" \
    response_time_ms "${tx_response_ms}" \
    response_latency_ms "$((tx_response_ms - tx_start_ms))"
  if wait_tx "${domain}" "${txhash}"; then
    tx_confirmed_ms="$(now_ms)"
    metric_event tx_confirmed \
      domain "${domain}" \
      txhash "${txhash}" \
      command "${tx_command_name}" \
      confirmed_time_ms "${tx_confirmed_ms}" \
      latency_from_submit_ms "$((tx_confirmed_ms - tx_start_ms))"
    return 0
  fi
  tx_failed_ms="$(now_ms)"
  metric_event tx_failed \
    domain "${domain}" \
    txhash "${txhash}" \
    command "${tx_command_name}" \
    failed_time_ms "${tx_failed_ms}" \
    latency_from_submit_ms "$((tx_failed_ms - tx_start_ms))"
  return 1
}

wait_channel() {
  local domain="$1" channel="$2" route="$3" label="$4" attempt channel_json

  echo "Waiting for ${route} ${label} actual=${domain}/${channel}..."
  for ((attempt = 1; attempt <= ${CROSSREF_CHANNEL_WAIT_RETRIES:-120}; attempt++)); do
    if channel_json="$(query_chain "${domain}" query ibc channel end crossref "${channel}" --output json 2>/dev/null)" &&
      printf '%s' "${channel_json}" | node -e '
        const value = JSON.parse(require("fs").readFileSync(0, "utf8"));
        process.exit(value.channel?.state === "STATE_OPEN" ? 0 : 1);
      '; then
      return 0
    fi
    sleep 3
  done
  echo "Timed out waiting for OPEN ${domain}/${channel}." >&2
  return 1
}

wait_reference() {
  local_domain="$1"
  remote_domain="$2"
  height="$3"
  route="$(route_id "${remote_domain}" "${local_domain}")"

  echo "${route} ${local_domain} stored cross-reference for ${remote_domain}:"
  attempt=1
  while [ "${attempt}" -le "${CROSSREF_VERIFY_RETRIES}" ]; do
    if query_chain "${local_domain}" query crossref cross-reference "${local_domain}" "${remote_domain}" "${height}" --output json; then
      metric_event reference_visible \
        route_id "${route}" \
        local_domain "${local_domain}" \
        remote_domain "${remote_domain}" \
        height "${height}" \
        attempt "${attempt}" \
        relayer_worker "$(route_worker "${remote_domain}" "${local_domain}")"
      return 0
    fi
    echo "Cross-reference ${local_domain}<-${remote_domain} not visible yet; retry ${attempt}/${CROSSREF_VERIFY_RETRIES}..."
    sleep "${CROSSREF_VERIFY_SLEEP_SECONDS}"
    attempt=$((attempt + 1))
  done

  query_chain "${local_domain}" query crossref cross-reference "${local_domain}" "${remote_domain}" "${height}" --output json
}

run_broadcast_for_source() {
  source_domain="$1"
  metric_event fanout_start \
    source_domain "${source_domain}" \
    checkpoint_height "${CHECKPOINT_HEIGHT}" \
    outbound_routes "$((ACTUAL_CHAIN_COUNT - 1))"
  if run_tx "${source_domain}" send-cross-reference-packet validator "${source_domain}" "${CHECKPOINT_HEIGHT}" \
    --all-bound-channels \
    --source-checkpoint-proof "$(proof_value "${source_domain}" source_checkpoint_proof)" \
    --source-proof-revision-number "$(proof_number "${source_domain}" source_proof_revision_number)" \
    --source-proof-revision-height "$(proof_number "${source_domain}" source_proof_revision_height)"; then
    metric_event fanout_confirmed \
      source_domain "${source_domain}" \
      checkpoint_height "${CHECKPOINT_HEIGHT}" \
      outbound_routes "$((ACTUAL_CHAIN_COUNT - 1))"
    return 0
  fi
  metric_event fanout_failed \
    source_domain "${source_domain}" \
    checkpoint_height "${CHECKPOINT_HEIGHT}"
  return 1
}

run_parallel_broadcasts() {
  tmp_dir="${TMPDIR:-/tmp}/crossref-broadcast-${ACTUAL_CHAIN_COUNT}c-${CHECKPOINT_HEIGHT}-$$"
  mkdir -p "${tmp_dir}"
  pids=""

  for source_domain in ${DOMAINS}; do
    log_file="${tmp_dir}/${source_domain}.log"
    (
      echo "Parallel broadcast start: ${source_domain}"
      run_broadcast_for_source "${source_domain}"
      echo "Parallel broadcast done: ${source_domain}"
    ) >"${log_file}" 2>&1 &
    pids="${pids} $!:${source_domain}:${log_file}"
  done

  failed=0
  for item in ${pids}; do
    pid="${item%%:*}"
    rest="${item#*:}"
    source_domain="${rest%%:*}"
    log_file="${rest#*:}"
    if wait "${pid}"; then
      cat "${log_file}"
    else
      failed=1
      echo "Parallel broadcast failed: ${source_domain}" >&2
      cat "${log_file}" >&2
    fi
  done

  if [ "${failed}" != "0" ]; then
    echo "One or more parallel broadcasts failed." >&2
    return 1
  fi
}

json_string_field() {
  key="$1"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | tail -1
}

json_number_field() {
  key="$1"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" | tail -1
}

checkpoint_proof_json() {
  domain="$1"
  checkpoint_height="$2"
  query_chain "${domain}" query crossref checkpoint-proof "${domain}" "${checkpoint_height}" --output json
}

b64() {
  printf '%s' "$1" | base64 | tr -d '\n'
}

hex_to_b64() {
  node -e '
    const hex = (process.argv[1] || "").trim();
    if (!/^[0-9a-fA-F]*$/.test(hex) || hex.length % 2 !== 0) {
      console.error(`invalid hex: ${hex}`);
      process.exit(2);
    }
    process.stdout.write(Buffer.from(hex, "hex").toString("base64"));
  ' "$1"
}

domain_suffix() {
  printf '%s\n' "${1#chain-}"
}

comet_block_json() {
  domain="$1"
  height="$2"
  service="$(service_for_domain "${domain}")"
  dc exec -T "${service}" curl -sf "http://localhost:26657/block?height=${height}"
}

comet_block_field() {
  domain="$1"
  height="$2"
  field="$3"
  comet_block_json "${domain}" "${height}" | node -e '
    const fs = require("fs");
    const input = JSON.parse(fs.readFileSync(0, "utf8"));
    const header = input?.result?.block?.header || {};
    const field = process.argv[1];
    const value = header[field];
    if (value === undefined || value === null || value === "") process.exit(1);
    process.stdout.write(String(value));
  ' "${field}"
}

comet_block_hash_b64() {
  domain="$1"
  height="$2"
  comet_block_json "${domain}" "${height}" | node -e '
    const crypto = require("crypto");
    const fs = require("fs");
    const input = JSON.parse(fs.readFileSync(0, "utf8"));
    const blockIdHash = input?.result?.block_id?.hash || "";
    if (/^[0-9a-fA-F]+$/.test(blockIdHash) && blockIdHash.length % 2 === 0) {
      process.stdout.write(Buffer.from(blockIdHash, "hex").toString("base64"));
      return;
    }
    const block = input?.result?.block;
    if (!block) process.exit(1);
    process.stdout.write(crypto.createHash("sha256").update(JSON.stringify(block)).digest("base64"));
  '
}

block_hash_for_domain() {
  domain="$1"
  if [ "${CROSSREF_USE_REAL_BLOCK_HASHES}" = "1" ]; then
    comet_block_hash_b64 "${domain}" "${CHECKPOINT_HEIGHT}"
  else
    b64 "block-$(domain_suffix "${domain}")-${CHECKPOINT_HEIGHT}"
  fi
}

app_hash_for_domain() {
  domain="$1"
  if [ "${CROSSREF_USE_REAL_BLOCK_HASHES}" = "1" ]; then
    hex_to_b64 "$(comet_block_field "${domain}" "${CHECKPOINT_HEIGHT}" app_hash)"
  else
    b64 "app-$(domain_suffix "${domain}")-${CHECKPOINT_HEIGHT}"
  fi
}

validator_set_hash_for_domain() {
  domain="$1"
  if [ "${CROSSREF_USE_REAL_BLOCK_HASHES}" = "1" ]; then
    hex_to_b64 "$(comet_block_field "${domain}" "${CHECKPOINT_HEIGHT}" validators_hash)"
  fi
}

block_time_unix_for_domain() {
  domain="$1"
  if [ "${BLOCK_TIME_UNIX}" != "0" ]; then
    printf '%s\n' "${BLOCK_TIME_UNIX}"
    return 0
  fi
  if [ "${CROSSREF_USE_REAL_BLOCK_HASHES}" = "1" ]; then
    comet_block_field "${domain}" "${CHECKPOINT_HEIGHT}" time | node -e '
      const chunks = [];
      process.stdin.on("data", (chunk) => chunks.push(chunk));
      process.stdin.on("end", () => process.stdout.write(String(Math.floor(new Date(Buffer.concat(chunks).toString().trim()).getTime() / 1000))));
    '
  else
    printf '0\n'
  fi
}

hysteresis_seed() {
  domain="$1"
  echo "crossref-topology-hysteresis-${domain}"
}

hysteresis_json() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  previous_hash="${5:-}"
  previous_state_hash="${6:-}"
  block_time_unix="${7:-${BLOCK_TIME_UNIX}}"
  cache_dir="${TMPDIR:-/tmp}/crossref-hysteresis-${ACTUAL_CHAIN_COUNT}c"
  mkdir -p "${cache_dir}"
  cache_key="$(printf '%s-%s-%s-%s-%s-%s-%s' "${domain}" "${height}" "${block_hash}" "${app_hash}" "${previous_hash:-genesis}" "${previous_state_hash:-genesis}" "${block_time_unix}" | tr -c 'A-Za-z0-9_.=-' '_')"
  cache_file="${cache_dir}/${cache_key}.json"
  if [ ! -f "${cache_file}" ]; then
    go run docker/scripts/hysteresis-sign.go "${domain}" "${height}" "${block_hash}" "${app_hash}" "${block_time_unix}" "$(hysteresis_seed "${domain}")" "${previous_hash}" "${previous_state_hash}" >"${cache_file}"
  fi
  cat "${cache_file}"
}

hysteresis_public_key() {
  domain="$1"
  json="$(hysteresis_json "${domain}" "${CHECKPOINT_HEIGHT}" "$(b64 block-template)" "$(b64 app-template)")"
  printf '%s\n' "${json}" | json_string_field public_key
}

hysteresis_signature() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  previous_hash="${5:-}"
  previous_state_hash="${6:-}"
  block_time_unix="${7:-${BLOCK_TIME_UNIX}}"
  json="$(hysteresis_json "${domain}" "${height}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state_hash}" "${block_time_unix}")"
  printf '%s\n' "${json}" | json_string_field signature
}

hysteresis_state_hash() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  previous_hash="${5:-}"
  previous_state_hash="${6:-}"
  block_time_unix="${7:-${BLOCK_TIME_UNIX}}"
  json="$(hysteresis_json "${domain}" "${height}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state_hash}" "${block_time_unix}")"
  printf '%s\n' "${json}" | json_string_field state_hash
}

hysteresis_consensus_proof() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  previous_hash="${5:-}"
  previous_state_hash="${6:-}"
  block_time_unix="${7:-${BLOCK_TIME_UNIX}}"
  json="$(hysteresis_json "${domain}" "${height}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state_hash}" "${block_time_unix}")"
  printf '%s\n' "${json}" | json_string_field consensus_proof
}

consensus_submit_flags() {
  if [ "${CROSSREF_REQUIRE_CONSENSUS_PROOF:-false}" = "true" ]; then
    printf '%s\n' "--consensus-proof $1 --consensus-proof-revision-number 1 --consensus-proof-revision-height $2"
  fi
}

comet_consensus_proof() {
  domain="$1"
  height="$2"
  rpc_url="$(topology_query rpc-url "${domain}")"
  go run ./docker/scripts/comet-consensus-proof "${rpc_url}" "${height}"
}

consensus_proof_for_domain() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  previous_hash="${5:-}"
  previous_state_hash="${6:-}"
  block_time_unix="${7:-${BLOCK_TIME_UNIX}}"
  case "${CROSSREF_CONSENSUS_PROOF_MODE}" in
    cometbft)
      comet_consensus_proof "${domain}" "${height}"
      ;;
    digest)
      hysteresis_consensus_proof "${domain}" "${height}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state_hash}" "${block_time_unix}"
      ;;
    *)
      echo "Unknown CROSSREF_CONSENSUS_PROOF_MODE=${CROSSREF_CONSENSUS_PROOF_MODE}; expected digest or cometbft." >&2
      return 1
      ;;
  esac
}

previous_checkpoint_hash() {
  domain="$1"
  height="$2"
  if [ "${height}" -le 1 ]; then
    return 0
  fi
  previous_height=$((height - 1))
  query_chain "${domain}" query crossref checkpoint "${domain}" "${previous_height}" --output json | json_string_field checkpoint_hash
}

previous_state_hash() {
  domain="$1"
  height="$2"
  if [ "${height}" -le 1 ]; then
    return 0
  fi
  previous_height=$((height - 1))
  query_chain "${domain}" query crossref checkpoint "${domain}" "${previous_height}" --output json | json_string_field state_hash
}

proof_file() {
  printf '%s/crossref-proof-%s-%s.json\n' "${TMPDIR:-/tmp}" "${ACTUAL_CHAIN_COUNT}c" "$1"
}

proof_value() {
  domain="$1"
  field="$2"
  json_string_field "${field}" <"$(proof_file "${domain}")"
}

proof_number() {
  domain="$1"
  field="$2"
  json_number_field "${field}" <"$(proof_file "${domain}")"
}

update_client_to_proof_height() {
  host_domain="$1"
  source_domain="$2"
  revision_height="$3"
  host_chain_id="$(chain_id "${host_domain}")"
  client_id="$(client_for_source "${host_domain}" "${source_domain}")"
  route="$(route_id "${source_domain}" "${host_domain}")"

  echo "Updating ${route} ${source_domain} -> ${host_domain} light client ${client_id} on ${host_chain_id} to proof height ${revision_height}..."
  relayer_for_route "${source_domain}" "${host_domain}" update client --host-chain "${host_chain_id}" --client "${client_id}" --height "${revision_height}"
}

require_proof_fields() {
  domain="$1"
  proof="$2"
  revision_number="$3"
  revision_height="$4"

  if [ -z "${proof}" ] || [ -z "${revision_number}" ] || [ -z "${revision_height}" ]; then
    echo "Could not parse ${domain} checkpoint proof." >&2
    exit 1
  fi
}

echo "Using topology ${TOPOLOGY_FILE}: ${ACTUAL_CHAIN_COUNT} chains, ${ROUTE_COUNT} directed routes."
echo "Compose file: ${COMPOSE_FILE}"

echo "Preparing deterministic Ed25519 hysteresis public keys..."
for domain in ${DOMAINS}; do
  public_key="$(hysteresis_public_key "${domain}")"
  echo "  ${domain}: ${public_key}"
done

echo "Directed route numbering:"
for route_pair in ${ROUTES}; do
  source="${route_pair%%:*}"
  dest="${route_pair##*:}"
  echo "  $(route_id "${source}" "${dest}") ${source}/$(channel_id "${source}" "${dest}") -> ${dest}/$(counterparty_channel_id "${source}" "${dest}") relayer-worker=$(route_worker "${source}" "${dest}")"
done

if [ "${DRY_RUN}" = "1" ]; then
  echo "Dry run complete; topology was parsed without contacting Docker."
  exit 0
fi

if [ "${CROSSREF_PHASE}" != "deliver" ]; then
  init_measurements
fi
trap 'exit_code="$?"; metric_event experiment_failed line "$LINENO" exit_code "${exit_code}"; exit "${exit_code}"' ERR

for route_pair in ${ROUTES}; do
  source="${route_pair%%:*}"
  dest="${route_pair##*:}"
  wait_channel "${source}" "$(channel_id "${source}" "${dest}")" "$(route_id "${source}" "${dest}")" "${source} -> ${dest} channel"
done

if [ "${CROSSREF_PHASE}" != "deliver" ]; then
if [ "${SKIP_REGISTER}" = "1" ]; then
  echo "Skipping domain registration because SKIP_REGISTER=1."
else
  echo "Registering all domains on all chains..."
  for local_domain in ${DOMAINS}; do
    for remote_domain in ${DOMAINS}; do
      remote_public_key="$(hysteresis_public_key "${remote_domain}")"
      remote_validator_set_hash="$(validator_set_hash_for_domain "${remote_domain}")"
      validator_set_flag=()
      if [ -n "${remote_validator_set_hash}" ]; then
        validator_set_flag=(--validator-set-hash "${remote_validator_set_hash}")
      fi
      echo "Registering ${remote_domain} on ${local_domain} with hysteresis public key ${remote_public_key}"
      run_tx "${local_domain}" register-domain validator "${remote_domain}" "$(chain_id "${remote_domain}")" --hysteresis-public-key "${remote_public_key}" "${validator_set_flag[@]}"
    done
  done
fi

if [ "${SKIP_BIND}" = "1" ]; then
  echo "Skipping channel binding because SKIP_BIND=1."
else
  echo "Binding crossref domains to topology IBC channels..."
  for route_pair in ${ROUTES}; do
    source="${route_pair%%:*}"
    dest="${route_pair##*:}"
    echo "Binding $(route_id "${source}" "${dest}") ${source} -> ${dest} actual=${source}/$(channel_id "${source}" "${dest}")"
    run_tx "${source}" bind-domain-channel validator "${source}" "${dest}" crossref "$(channel_id "${source}" "${dest}")"
  done
fi

if [ "${SKIP_CHECKPOINT_SUBMIT}" = "1" ]; then
  echo "Skipping checkpoint submission because SKIP_CHECKPOINT_SUBMIT=1."
else
  echo "Submitting checkpoints on all ${ACTUAL_CHAIN_COUNT} chains..."
  for domain in ${DOMAINS}; do
    block_hash="$(block_hash_for_domain "${domain}")"
    app_hash="$(app_hash_for_domain "${domain}")"
    checkpoint_block_time="$(block_time_unix_for_domain "${domain}")"
    previous_hash="$(previous_checkpoint_hash "${domain}" "${CHECKPOINT_HEIGHT}")"
    previous_state="$(previous_state_hash "${domain}" "${CHECKPOINT_HEIGHT}")"
    state_hash="$(hysteresis_state_hash "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state}" "${checkpoint_block_time}")"
    consensus_proof="$(consensus_proof_for_domain "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state}" "${checkpoint_block_time}")"
    consensus_flags="$(consensus_submit_flags "${consensus_proof}" "${CHECKPOINT_HEIGHT}")"
    signature="$(hysteresis_signature "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" "${previous_hash}" "${previous_state}" "${checkpoint_block_time}")"
    if [ -n "${previous_hash}" ]; then
      # shellcheck disable=SC2086
      run_tx "${domain}" submit-checkpoint validator "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" --previous-checkpoint-hash "${previous_hash}" --previous-state-hash "${previous_state}" --state-hash "${state_hash}" --hysteresis-signature "${signature}" --block-time-unix "${checkpoint_block_time}" --key-epoch 1 ${consensus_flags}
    else
      # shellcheck disable=SC2086
      run_tx "${domain}" submit-checkpoint validator "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" --state-hash "${state_hash}" --hysteresis-signature "${signature}" --block-time-unix "${checkpoint_block_time}" --key-epoch 1 ${consensus_flags}
    fi
  done
fi

echo "Collecting checkpoint ICS23 proofs from source chains..."
for domain in ${DOMAINS}; do
  checkpoint_proof_json "${domain}" "${CHECKPOINT_HEIGHT}" >"$(proof_file "${domain}")"
  cat "$(proof_file "${domain}")"
  require_proof_fields \
    "${domain}" \
    "$(proof_value "${domain}" source_checkpoint_proof)" \
    "$(proof_number "${domain}" source_proof_revision_number)" \
    "$(proof_number "${domain}" source_proof_revision_height)"
done

echo "Updating destination light clients to source proof heights..."
for source_domain in ${DOMAINS}; do
  revision_height="$(proof_number "${source_domain}" source_proof_revision_height)"
  for host_domain in ${DOMAINS}; do
    if [ "${host_domain}" != "${source_domain}" ]; then
      update_client_to_proof_height "${host_domain}" "${source_domain}" "${revision_height}"
    fi
  done
done

if [ "${CROSSREF_PHASE}" = "prepare" ]; then
  node docker/scripts/crossref-preparation.mjs create
  metric_event preparation_complete
  exit 0
fi
else
  node docker/scripts/crossref-preparation.mjs consume
  metric_event delivery_start
fi

if [ "${CROSSREF_START_RELAYER_BEFORE_BROADCAST}" = "1" ]; then
  echo "Starting relayer service before packet broadcast..."
  start_relayer_services
fi
if [ "${CROSSREF_RELAY_AFTER_BROADCAST}" = "one-shot" ] || [ "${CROSSREF_RELAY_AFTER_BROADCAST}" = "controller" ]; then
  echo "Stopping relayer services before packet broadcast; ${CROSSREF_RELAY_AFTER_BROADCAST} relay mode will deliver after broadcast."
  stop_relayer_services
fi

echo "Submitting crossref sendPacket fan-out tx from all ${ACTUAL_CHAIN_COUNT} source chains..."
case "${CROSSREF_BROADCAST_MODE}" in
  parallel)
    echo "Using parallel broadcast mode: all source chains submit sendPacket fan-out tx concurrently."
    run_parallel_broadcasts
    ;;
  sequential)
    echo "Using sequential broadcast mode: one source chain fan-out tx at a time."
    for source_domain in ${DOMAINS}; do
      run_broadcast_for_source "${source_domain}"
    done
    ;;
  *)
    echo "Unknown CROSSREF_BROADCAST_MODE=${CROSSREF_BROADCAST_MODE}; expected sequential or parallel." >&2
    exit 1
    ;;
esac

if [ "${CROSSREF_RELAY_AFTER_BROADCAST}" = "one-shot" ]; then
  relay_all_routes_once
elif [ "${CROSSREF_RELAY_AFTER_BROADCAST}" = "controller" ]; then
  relay_all_routes_with_controller
fi

if [ "${CROSSREF_RELAY_SETTLE_SECONDS}" -gt 0 ]; then
  sleep "${CROSSREF_RELAY_SETTLE_SECONDS}"
fi

echo "Verifying directed cross-references on all destination chains..."
for local_domain in ${DOMAINS}; do
  for remote_domain in ${DOMAINS}; do
    if [ "${local_domain}" != "${remote_domain}" ]; then
      wait_reference "${local_domain}" "${remote_domain}" "${CHECKPOINT_HEIGHT}"
    fi
  done
done

finalize_measurements
trap - ERR

echo "${ACTUAL_CHAIN_COUNT}-chain cross-reference experiment passed."
