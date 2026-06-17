#!/usr/bin/env bash
set -euo pipefail

CHAIN_COUNT="${CHAIN_COUNT:-5}"
RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT:-1}"
TOPOLOGY_FILE="${TOPOLOGY_FILE:-docker/generated/topology-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r.json}"
COMPOSE_FILE="${COMPOSE_FILE:-docker/docker-compose.yml}"
RELAYER_SERVICE="${RELAYER_SERVICE:-relayer}"
RELAYER_INDEX="${RELAYER_INDEX:-1}"
DENOM="${DENOM:-stake}"
BLOCK_TIME_UNIX="${BLOCK_TIME_UNIX:-0}"
CHECKPOINT_HEIGHT="${CHECKPOINT_HEIGHT:-1}"
DRY_RUN="${DRY_RUN:-0}"

if [ ! -f "${TOPOLOGY_FILE}" ]; then
  echo "Topology ${TOPOLOGY_FILE} not found; generating ${CHAIN_COUNT} chains / ${RELAYER_WORKER_COUNT} relayers..."
  node docker/scripts/generate-topology.mjs "${CHAIN_COUNT}" "${RELAYER_WORKER_COUNT}" >/dev/null
fi

if [ ! -f "${TOPOLOGY_FILE}" ]; then
  echo "Topology file not found after generation: ${TOPOLOGY_FILE}" >&2
  exit 1
fi

dc() {
  docker compose -f "${COMPOSE_FILE}" "$@"
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

chain_id() {
  topology_query chain-id "$1"
}

service_for_domain() {
  topology_query service "$1"
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
  host_domain="$1"
  source_domain="$2"
  host_channel="$(channel_id "${host_domain}" "${source_domain}")"
  case "${host_channel}" in
    channel-*) printf '07-tendermint-%s\n' "${host_channel#channel-}" ;;
    *) echo "unknown host channel for ${host_domain}<-${source_domain}" >&2; return 1 ;;
  esac
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
  worker="$(route_worker "${source_domain}" "${host_domain}")"
  if relayer_service_exists "${RELAYER_SERVICE}"; then
    dc exec -T --index "${worker}" "${RELAYER_SERVICE}" hermes "${@:3}"
  elif relayer_service_exists "relayer-${worker}"; then
    dc exec -T "relayer-${worker}" hermes "${@:3}"
  else
    dc exec -T --index "${RELAYER_INDEX}" "${RELAYER_SERVICE}" hermes "${@:3}"
  fi
}

tx_chain() {
  domain="$1"
  shift
  query_chain "${domain}" tx crossref "$@" --from validator --chain-id "$(chain_id "${domain}")" --keyring-backend test --yes --fees "0${DENOM}"
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
  out="$(tx_chain "${domain}" "$@" --output json 2>&1)"
  printf '%s\n' "${out}"
  txhash="$(printf '%s\n' "${out}" | sed -n 's/.*"txhash"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | tail -1)"
  if [ -z "${txhash}" ]; then
    echo "Could not parse ${domain} txhash." >&2
    return 1
  fi
  wait_tx "${domain}" "${txhash}"
}

wait_channel() {
  domain="$1"
  channel="$2"
  route="$3"
  label="$4"

  echo "Waiting for ${route} ${label} actual=${domain}/${channel}..."
  until query_chain "${domain}" query ibc channel channels --output json | grep -q "${channel}"; do
    sleep 3
  done
}

wait_reference() {
  local_domain="$1"
  remote_domain="$2"
  height="$3"
  route="$(route_id "${remote_domain}" "${local_domain}")"

  echo "${route} ${local_domain} stored cross-reference for ${remote_domain}:"
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if query_chain "${local_domain}" query crossref cross-reference "${local_domain}" "${remote_domain}" "${height}" --output json; then
      return 0
    fi
    echo "Cross-reference ${local_domain}<-${remote_domain} not visible yet; retry ${attempt}/12..."
    sleep 5
  done

  query_chain "${local_domain}" query crossref cross-reference "${local_domain}" "${remote_domain}" "${height}" --output json
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

domain_suffix() {
  printf '%s\n' "${1#chain-}"
}

block_hash_for_domain() {
  b64 "block-$(domain_suffix "$1")-${CHECKPOINT_HEIGHT}"
}

app_hash_for_domain() {
  b64 "app-$(domain_suffix "$1")-${CHECKPOINT_HEIGHT}"
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
  cache_dir="${TMPDIR:-/tmp}/crossref-hysteresis-${ACTUAL_CHAIN_COUNT}c"
  mkdir -p "${cache_dir}"
  cache_file="${cache_dir}/${domain}-${height}-${block_hash}-${app_hash}-${BLOCK_TIME_UNIX}.json"
  if [ ! -f "${cache_file}" ]; then
    go run docker/scripts/hysteresis-sign.go "${domain}" "${height}" "${block_hash}" "${app_hash}" "${BLOCK_TIME_UNIX}" "$(hysteresis_seed "${domain}")" >"${cache_file}"
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
  json="$(hysteresis_json "${domain}" "${height}" "${block_hash}" "${app_hash}")"
  printf '%s\n' "${json}" | json_string_field signature
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

for route_pair in ${ROUTES}; do
  source="${route_pair%%:*}"
  dest="${route_pair##*:}"
  wait_channel "${source}" "$(channel_id "${source}" "${dest}")" "$(route_id "${source}" "${dest}")" "${source} -> ${dest} channel"
done

echo "Registering all domains on all chains..."
for local_domain in ${DOMAINS}; do
  for remote_domain in ${DOMAINS}; do
    remote_public_key="$(hysteresis_public_key "${remote_domain}")"
    echo "Registering ${remote_domain} on ${local_domain} with hysteresis public key ${remote_public_key}"
    run_tx "${local_domain}" register-domain validator "${remote_domain}" "$(chain_id "${remote_domain}")" --hysteresis-public-key "${remote_public_key}"
  done
done

echo "Binding crossref domains to topology IBC channels..."
for route_pair in ${ROUTES}; do
  source="${route_pair%%:*}"
  dest="${route_pair##*:}"
  echo "Binding $(route_id "${source}" "${dest}") ${source} -> ${dest} actual=${source}/$(channel_id "${source}" "${dest}")"
  run_tx "${source}" bind-domain-channel validator "${source}" "${dest}" crossref "$(channel_id "${source}" "${dest}")"
done

echo "Submitting checkpoints on all ${ACTUAL_CHAIN_COUNT} chains..."
for domain in ${DOMAINS}; do
  block_hash="$(block_hash_for_domain "${domain}")"
  app_hash="$(app_hash_for_domain "${domain}")"
  signature="$(hysteresis_signature "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}")"
  run_tx "${domain}" submit-checkpoint validator "${domain}" "${CHECKPOINT_HEIGHT}" "${block_hash}" "${app_hash}" --hysteresis-signature "${signature}" --block-time-unix "${BLOCK_TIME_UNIX}"
done

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

echo "Broadcasting cross-reference packets from all ${ACTUAL_CHAIN_COUNT} chains..."
for source_domain in ${DOMAINS}; do
  run_tx "${source_domain}" broadcast-cross-reference-packet validator "${source_domain}" "${CHECKPOINT_HEIGHT}" \
    --source-checkpoint-proof "$(proof_value "${source_domain}" source_checkpoint_proof)" \
    --source-proof-revision-number "$(proof_number "${source_domain}" source_proof_revision_number)" \
    --source-proof-revision-height "$(proof_number "${source_domain}" source_proof_revision_height)"
done

sleep 20

echo "Verifying directed cross-references on all destination chains..."
for local_domain in ${DOMAINS}; do
  for remote_domain in ${DOMAINS}; do
    if [ "${local_domain}" != "${remote_domain}" ]; then
      wait_reference "${local_domain}" "${remote_domain}" "${CHECKPOINT_HEIGHT}"
    fi
  done
done

echo "${ACTUAL_CHAIN_COUNT}-chain cross-reference experiment passed."
