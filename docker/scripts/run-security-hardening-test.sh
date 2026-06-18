#!/usr/bin/env bash
set -euo pipefail

CHAIN_COUNT="${CHAIN_COUNT:-3}"
RELAYER_WORKER_COUNT="${RELAYER_WORKER_COUNT:-2}"
TOPOLOGY_FILE="${TOPOLOGY_FILE:-docker/generated/topology-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r.json}"
COMPOSE_FILE="${COMPOSE_FILE:-docker/generated/docker-compose-${CHAIN_COUNT}c-${RELAYER_WORKER_COUNT}r.yml}"
DENOM="${DENOM:-stake}"
BLOCK_TIME_UNIX="${BLOCK_TIME_UNIX:-0}"
ROTATION_HEIGHT="${ROTATION_HEIGHT:-2}"
THRESHOLD_HEIGHT="${THRESHOLD_HEIGHT:-2}"
BAD_PROOF_HEIGHT="${BAD_PROOF_HEIGHT:-2}"
REPLAY_HEIGHT="${REPLAY_HEIGHT:-1}"
SKIP_REPLAY="${SKIP_REPLAY:-0}"
SKIP_ROTATION="${SKIP_ROTATION:-0}"
SKIP_THRESHOLD="${SKIP_THRESHOLD:-0}"
SKIP_BAD_PROOF="${SKIP_BAD_PROOF:-0}"

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
    else if (query === "chain-count") console.log(topology.chains.length);
    else if (query === "chain-id") console.log(chain(a)?.chainId || "");
    else if (query === "service") console.log(chain(a)?.service || a);
    else if (query === "channel") console.log(route(a, b)?.fromChannel || "");
    else if (query === "worker") console.log(route(a, b)?.relayerWorker || 1);
    else {
      console.error(`unknown topology query: ${query}`);
      process.exit(2);
    }
  ' "${TOPOLOGY_FILE}" "$@"
}

DOMAINS="$(topology_query domains)"
ACTUAL_CHAIN_COUNT="$(topology_query chain-count)"
if [ "${ACTUAL_CHAIN_COUNT}" -lt 3 ]; then
  echo "Security hardening test requires at least 3 chains." >&2
  exit 1
fi

read -r DOMAIN_A DOMAIN_B DOMAIN_C _ <<EOF_DOMAINS
${DOMAINS}
EOF_DOMAINS

chain_id() {
  topology_query chain-id "$1"
}

service_for_domain() {
  topology_query service "$1"
}

channel_id() {
  topology_query channel "$1" "$2"
}

route_worker() {
  topology_query worker "$1" "$2"
}

query_chain() {
  domain="$1"
  shift
  dc exec -T "$(service_for_domain "${domain}")" crossrefd --home /var/crossref "$@"
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

expect_tx_failure() {
  domain="$1"
  expected="$2"
  shift 2
  set +e
  out="$(tx_chain "${domain}" "$@" --output json 2>&1)"
  code=$?
  set -e
  printf '%s\n' "${out}"
  if [ "${code}" -eq 0 ]; then
    txhash="$(printf '%s\n' "${out}" | sed -n 's/.*"txhash"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | tail -1)"
    if [ -n "${txhash}" ]; then
      attempt=1
      while [ "${attempt}" -le 30 ]; do
        if wait_out="$(query_chain "${domain}" query tx "${txhash}" --output json 2>&1)"; then
          printf '%s\n' "${wait_out}"
          if printf '%s\n' "${wait_out}" | grep -Eq '"code"[[:space:]]*:[[:space:]]*"?0"?'; then
            echo "Expected ${domain} tx to fail with ${expected}, but it succeeded." >&2
            exit 1
          fi
          if ! printf '%s\n' "${wait_out}" | grep -q "${expected}"; then
            echo "Expected failure containing ${expected}." >&2
            exit 1
          fi
          return 0
        fi
        sleep 2
        attempt=$((attempt + 1))
      done
    fi
    echo "Expected ${domain} tx to fail with ${expected}, but no failing tx was found." >&2
    exit 1
  fi
  if ! printf '%s\n' "${out}" | grep -q "${expected}"; then
    echo "Expected failure containing ${expected}." >&2
    exit 1
  fi
}

json_string_field() {
  key="$1"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | tail -1
}

b64() {
  printf '%s' "$1" | base64 | tr -d '\n'
}

domain_suffix() {
  printf '%s\n' "${1#chain-}"
}

block_hash_for_domain_height() {
  b64 "block-$(domain_suffix "$1")-$2"
}

app_hash_for_domain_height() {
  b64 "app-$(domain_suffix "$1")-$2"
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

hysteresis_json() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  seed="$5"
  previous_hash="${6:-}"
  go run docker/scripts/hysteresis-sign.go "${domain}" "${height}" "${block_hash}" "${app_hash}" "${BLOCK_TIME_UNIX}" "${seed}" "${previous_hash}"
}

hysteresis_public_key() {
  domain="$1"
  seed="$2"
  hysteresis_json "${domain}" "1" "$(b64 block-template)" "$(b64 app-template)" "${seed}" | json_string_field public_key
}

hysteresis_signature() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  seed="$5"
  previous_hash="${6:-}"
  hysteresis_json "${domain}" "${height}" "${block_hash}" "${app_hash}" "${seed}" "${previous_hash}" | json_string_field signature
}

threshold_json() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  signer_count="$5"
  previous_hash="${6:-}"
  go run ./docker/scripts/hysteresis-threshold-sign "${domain}" "${height}" "${block_hash}" "${app_hash}" "${BLOCK_TIME_UNIX}" 2 3 "${signer_count}" "crossref-threshold-${domain}" "${previous_hash}"
}

threshold_public_key() {
  domain="$1"
  threshold_json "${domain}" "1" "$(b64 block-template)" "$(b64 app-template)" 2 | json_string_field public_key
}

threshold_signature() {
  domain="$1"
  height="$2"
  block_hash="$3"
  app_hash="$4"
  signer_count="$5"
  previous_hash="${6:-}"
  threshold_json "${domain}" "${height}" "${block_hash}" "${app_hash}" "${signer_count}" "${previous_hash}" | json_string_field signature
}

checkpoint_proof_json() {
  domain="$1"
  height="$2"
  query_chain "${domain}" query crossref checkpoint-proof "${domain}" "${height}" --output json
}

proof_field() {
  field="$1"
  json_string_field "${field}"
}

assert_no_cross_reference() {
  local_domain="$1"
  remote_domain="$2"
  height="$3"
  if query_chain "${local_domain}" query crossref cross-reference "${local_domain}" "${remote_domain}" "${height}" --output json >/tmp/crossref-hardening-query.out 2>&1; then
    cat /tmp/crossref-hardening-query.out
    echo "Unexpected cross-reference ${local_domain}<-${remote_domain} height ${height}." >&2
    exit 1
  fi
  cat /tmp/crossref-hardening-query.out
}

echo "Running security hardening test on ${TOPOLOGY_FILE} using ${DOMAIN_A}, ${DOMAIN_B}, ${DOMAIN_C}."

if [ "${SKIP_REPLAY}" = "1" ]; then
  echo "1. Skipping replay check because SKIP_REPLAY=1."
else
  echo "1. Verifying replayed broadcast of ${DOMAIN_A} height ${REPLAY_HEIGHT} does not create a duplicate cross-reference."
proof_json="$(checkpoint_proof_json "${DOMAIN_A}" "${REPLAY_HEIGHT}")"
replay_proof="$(printf '%s\n' "${proof_json}" | proof_field source_checkpoint_proof)"
replay_revision_number="$(printf '%s\n' "${proof_json}" | sed -n 's/.*"source_proof_revision_number"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | tail -1)"
replay_revision_height="$(printf '%s\n' "${proof_json}" | sed -n 's/.*"source_proof_revision_height"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | tail -1)"
run_tx "${DOMAIN_A}" broadcast-cross-reference-packet validator "${DOMAIN_A}" "${REPLAY_HEIGHT}" --source-checkpoint-proof "${replay_proof}" --source-proof-revision-number "${replay_revision_number}" --source-proof-revision-height "${replay_revision_height}"
sleep 15
query_chain "${DOMAIN_B}" query crossref cross-reference "${DOMAIN_B}" "${DOMAIN_A}" "${REPLAY_HEIGHT}" --output json
query_chain "${DOMAIN_C}" query crossref cross-reference "${DOMAIN_C}" "${DOMAIN_A}" "${REPLAY_HEIGHT}" --output json
fi

if [ "${SKIP_ROTATION}" = "1" ]; then
  echo "2. Skipping hysteresis key rotation because SKIP_ROTATION=1."
else
  echo "2. Verifying hysteresis key rotation on ${DOMAIN_A}."
ROTATED_SEED="crossref-rotated-${DOMAIN_A}"
ROTATED_PUBLIC_KEY="$(hysteresis_public_key "${DOMAIN_A}" "${ROTATED_SEED}")"
for local_domain in ${DOMAINS}; do
  run_tx "${local_domain}" register-domain validator "${DOMAIN_A}" "$(chain_id "${DOMAIN_A}")" --hysteresis-public-key "${ROTATED_PUBLIC_KEY}"
done
rotation_block_hash="$(block_hash_for_domain_height "${DOMAIN_A}" "${ROTATION_HEIGHT}")"
rotation_app_hash="$(app_hash_for_domain_height "${DOMAIN_A}" "${ROTATION_HEIGHT}")"
rotation_previous_hash="$(previous_checkpoint_hash "${DOMAIN_A}" "${ROTATION_HEIGHT}")"
rotation_signature="$(hysteresis_signature "${DOMAIN_A}" "${ROTATION_HEIGHT}" "${rotation_block_hash}" "${rotation_app_hash}" "${ROTATED_SEED}" "${rotation_previous_hash}")"
run_tx "${DOMAIN_A}" submit-checkpoint validator "${DOMAIN_A}" "${ROTATION_HEIGHT}" "${rotation_block_hash}" "${rotation_app_hash}" --previous-checkpoint-hash "${rotation_previous_hash}" --hysteresis-signature "${rotation_signature}" --block-time-unix "${BLOCK_TIME_UNIX}"

old_seed="crossref-topology-hysteresis-${DOMAIN_A}"
next_height=$((ROTATION_HEIGHT + 1))
next_block_hash="$(block_hash_for_domain_height "${DOMAIN_A}" "${next_height}")"
next_app_hash="$(app_hash_for_domain_height "${DOMAIN_A}" "${next_height}")"
next_previous_hash="$(previous_checkpoint_hash "${DOMAIN_A}" "${next_height}")"
old_signature="$(hysteresis_signature "${DOMAIN_A}" "${next_height}" "${next_block_hash}" "${next_app_hash}" "${old_seed}" "${next_previous_hash}")"
expect_tx_failure "${DOMAIN_A}" "hysteresis signature" submit-checkpoint validator "${DOMAIN_A}" "${next_height}" "${next_block_hash}" "${next_app_hash}" --previous-checkpoint-hash "${next_previous_hash}" --hysteresis-signature "${old_signature}" --block-time-unix "${BLOCK_TIME_UNIX}"
fi

if [ "${SKIP_THRESHOLD}" = "1" ]; then
  echo "3. Skipping threshold signature because SKIP_THRESHOLD=1."
else
  echo "3. Verifying 2-of-3 threshold hysteresis signature on ${DOMAIN_B}."
THRESHOLD_PUBLIC_KEY="$(threshold_public_key "${DOMAIN_B}")"
for local_domain in ${DOMAINS}; do
  run_tx "${local_domain}" register-domain validator "${DOMAIN_B}" "$(chain_id "${DOMAIN_B}")" --hysteresis-public-key "${THRESHOLD_PUBLIC_KEY}"
done
threshold_block_hash="$(block_hash_for_domain_height "${DOMAIN_B}" "${THRESHOLD_HEIGHT}")"
threshold_app_hash="$(app_hash_for_domain_height "${DOMAIN_B}" "${THRESHOLD_HEIGHT}")"
threshold_previous_hash="$(previous_checkpoint_hash "${DOMAIN_B}" "${THRESHOLD_HEIGHT}")"
threshold_good_signature="$(threshold_signature "${DOMAIN_B}" "${THRESHOLD_HEIGHT}" "${threshold_block_hash}" "${threshold_app_hash}" 2 "${threshold_previous_hash}")"
run_tx "${DOMAIN_B}" submit-checkpoint validator "${DOMAIN_B}" "${THRESHOLD_HEIGHT}" "${threshold_block_hash}" "${threshold_app_hash}" --previous-checkpoint-hash "${threshold_previous_hash}" --hysteresis-signature "${threshold_good_signature}" --block-time-unix "${BLOCK_TIME_UNIX}"

threshold_next_height=$((THRESHOLD_HEIGHT + 1))
threshold_next_block_hash="$(block_hash_for_domain_height "${DOMAIN_B}" "${threshold_next_height}")"
threshold_next_app_hash="$(app_hash_for_domain_height "${DOMAIN_B}" "${threshold_next_height}")"
threshold_next_previous_hash="$(previous_checkpoint_hash "${DOMAIN_B}" "${threshold_next_height}")"
threshold_bad_signature="$(threshold_signature "${DOMAIN_B}" "${threshold_next_height}" "${threshold_next_block_hash}" "${threshold_next_app_hash}" 1 "${threshold_next_previous_hash}")"
expect_tx_failure "${DOMAIN_B}" "valid_signatures=1 threshold=2" submit-checkpoint validator "${DOMAIN_B}" "${threshold_next_height}" "${threshold_next_block_hash}" "${threshold_next_app_hash}" --previous-checkpoint-hash "${threshold_next_previous_hash}" --hysteresis-signature "${threshold_bad_signature}" --block-time-unix "${BLOCK_TIME_UNIX}"
fi

if [ "${SKIP_BAD_PROOF}" = "1" ]; then
  echo "4. Skipping invalid proof because SKIP_BAD_PROOF=1."
else
  echo "4. Verifying invalid source checkpoint proof is not accepted on ${DOMAIN_A}<-${DOMAIN_C}."
bad_block_hash="$(block_hash_for_domain_height "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}")"
bad_app_hash="$(app_hash_for_domain_height "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}")"
bad_previous_hash="$(previous_checkpoint_hash "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}")"
bad_signature="$(hysteresis_signature "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}" "${bad_block_hash}" "${bad_app_hash}" "crossref-topology-hysteresis-${DOMAIN_C}" "${bad_previous_hash}")"
run_tx "${DOMAIN_C}" submit-checkpoint validator "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}" "${bad_block_hash}" "${bad_app_hash}" --previous-checkpoint-hash "${bad_previous_hash}" --hysteresis-signature "${bad_signature}" --block-time-unix "${BLOCK_TIME_UNIX}"
bad_proof_b64="$(b64 "not-a-valid-ics23-proof")"
run_tx "${DOMAIN_C}" send-cross-reference-packet validator "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}" crossref "$(channel_id "${DOMAIN_C}" "${DOMAIN_A}")" --source-checkpoint-proof "${bad_proof_b64}" --source-proof-revision-number 0 --source-proof-revision-height 1
sleep 15
assert_no_cross_reference "${DOMAIN_A}" "${DOMAIN_C}" "${BAD_PROOF_HEIGHT}"
fi

echo "Security hardening Docker test passed."
