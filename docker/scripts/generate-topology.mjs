#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const chainCount = positiveInt(process.env.CHAIN_COUNT ?? process.argv[2] ?? "5", "CHAIN_COUNT");
const relayerCount = positiveInt(process.env.RELAYER_COUNT ?? process.argv[3] ?? "1", "RELAYER_COUNT");
const hostPortOffset = nonNegativeInt(process.env.HOST_PORT_OFFSET ?? "0", "HOST_PORT_OFFSET");
const channelMode = enumValue(process.env.CHANNEL_MODE ?? "bidirectional", "CHANNEL_MODE", ["bidirectional", "directed"]);
const relayerShardMode = enumValue(process.env.RELAYER_SHARD_MODE ?? "pair", "RELAYER_SHARD_MODE", ["pair", "destination"]);
const hermesMaxMsgNum = positiveInt(process.env.HERMES_MAX_MSG_NUM ?? "1", "HERMES_MAX_MSG_NUM");
const hermesSequentialBatchTx = boolValue(process.env.HERMES_SEQUENTIAL_BATCH_TX ?? "true", "HERMES_SEQUENTIAL_BATCH_TX");
const hermesClearInterval = nonNegativeInt(process.env.HERMES_CLEAR_INTERVAL ?? "100", "HERMES_CLEAR_INTERVAL");
const hermesClearOnStart = boolValue(process.env.HERMES_CLEAR_ON_START ?? "true", "HERMES_CLEAR_ON_START");
const outDir = path.join(root, "docker", "generated");

if (chainCount < 2) {
  throw new Error("CHAIN_COUNT must be at least 2");
}
if (relayerShardMode === "destination" && channelMode !== "directed") {
  throw new Error("RELAYER_SHARD_MODE=destination requires CHANNEL_MODE=directed because Hermes packet filters are channel-scoped");
}

fs.mkdirSync(outDir, { recursive: true });

const mnemonic =
  "flight theme grace pulp vocal pistol slight assume major blossom upgrade solution picture script behind human whip rule goat raw search cube breeze hundred";
const relayerMnemonic =
  "turtle all pyramid produce tray equip elder border rose risk slight panther grant price smile try fly street carpet wage among hole join suit";

const chains = Array.from({ length: chainCount }, (_, index) => {
  const suffix = suffixFor(index);
  return {
    index,
    suffix,
    service: `chain-${suffix}`,
    domain: `chain-${suffix}`,
    chainId: `crossref-${suffix}`,
    rpcPort: 26657 + index * 10 + hostPortOffset,
    grpcPort: 9090 + index + hostPortOffset,
    apiPort: 1317 + index + hostPortOffset,
  };
});

const requestedRelayerCount = relayerCount;
const effectiveRelayerCount = relayerShardMode === "destination" ? chainCount : relayerCount;
const pairs = [];
const channelCounters = Object.fromEntries(chains.map((chain) => [chain.domain, 0]));
let routeIndex = 0;
let pairIndex = 0;

if (channelMode === "directed") {
  for (const left of chains) {
    for (const right of chains) {
      if (left.domain === right.domain) continue;
      const leftChannel = `channel-${channelCounters[left.domain]++}`;
      const rightChannel = `channel-${channelCounters[right.domain]++}`;
      const relayerWorker = relayerWorkerFor(left, right, pairIndex++, effectiveRelayerCount);
      pairs.push({
        left: left.domain,
        right: right.domain,
        leftChainId: left.chainId,
        rightChainId: right.chainId,
        leftChannel,
        rightChannel,
        directed: true,
        routes: [
          route(left, right, leftChannel, rightChannel, routeIndex++, relayerWorker),
        ],
      });
    }
  }
} else {
  for (let i = 0; i < chains.length; i++) {
    for (let j = i + 1; j < chains.length; j++) {
      const left = chains[i];
      const right = chains[j];
      const leftChannel = `channel-${channelCounters[left.domain]++}`;
      const rightChannel = `channel-${channelCounters[right.domain]++}`;
      const relayerWorker = relayerWorkerFor(left, right, pairIndex++, effectiveRelayerCount);
      pairs.push({
        left: left.domain,
        right: right.domain,
        leftChainId: left.chainId,
        rightChainId: right.chainId,
        leftChannel,
        rightChannel,
        directed: false,
        routes: [
          route(left, right, leftChannel, rightChannel, routeIndex++, relayerWorker),
          route(right, left, rightChannel, leftChannel, routeIndex++, relayerWorker),
        ],
      });
    }
  }
}

const routes = pairs.flatMap((pair) => pair.routes);
const topology = {
  chainCount,
  relayerCount: effectiveRelayerCount,
  requestedRelayerCount,
  channelMode,
  relayerShardMode,
  chains,
  pairs,
  routes,
  channelPairs: pairs.map((pair) => `${pair.leftChainId}:${pair.rightChainId}`),
};

const stem = topologyStem(chainCount, effectiveRelayerCount, channelMode, relayerShardMode);
fs.writeFileSync(path.join(outDir, `topology-${stem}.json`), `${JSON.stringify(topology, null, 2)}\n`);
fs.writeFileSync(path.join(outDir, `hermes-${stem}.toml`), hermesConfig(chains));
for (let worker = 1; worker <= effectiveRelayerCount; worker++) {
  fs.writeFileSync(
    path.join(outDir, `hermes-${stem}-worker-${worker}.toml`),
    hermesConfig(chains, routes.filter((r) => r.relayerWorker === worker), `relayer-worker-${worker}`),
  );
}
fs.writeFileSync(path.join(outDir, `docker-compose-${stem}.yml`), compose(topology, stem));

console.log(JSON.stringify({
  topology: path.relative(root, path.join(outDir, `topology-${stem}.json`)),
  compose: path.relative(root, path.join(outDir, `docker-compose-${stem}.yml`)),
  hermes: path.relative(root, path.join(outDir, `hermes-${stem}.toml`)),
  chainCount,
  relayerCount: effectiveRelayerCount,
  requestedRelayerCount,
  channelMode,
  relayerShardMode,
  routeCount: routes.length,
  hostPortOffset,
  hermesMaxMsgNum,
  hermesSequentialBatchTx,
  hermesClearInterval,
  hermesClearOnStart,
}, null, 2));

function relayerWorkerFor(_from, to, index, workerCount) {
  if (relayerShardMode === "destination") return to.index + 1;
  return (index % workerCount) + 1;
}

function route(from, to, fromChannel, toChannel, index, relayerWorker) {
  return {
    id: `route-${String(index).padStart(2, "0")}`,
    from: from.domain,
    to: to.domain,
    fromChainId: from.chainId,
    toChainId: to.chainId,
    fromChannel,
    toChannel,
    relayerWorker,
  };
}

function topologyStem(chainCount, workerCount, channelMode, shardMode) {
  const base = `${chainCount}c-${workerCount}r`;
  if (channelMode === "bidirectional" && shardMode === "pair") return base;
  return `${base}-${shardMode}-${channelMode}`;
}

function hermesConfig(chainList, allowedRoutes = null, keyName = "relayer") {
  const byChain = new Map();
  if (allowedRoutes) {
    for (const chain of chainList) byChain.set(chain.chainId, new Set());
    for (const route of allowedRoutes) {
      byChain.get(route.fromChainId)?.add(route.fromChannel);
      byChain.get(route.toChainId)?.add(route.toChannel);
    }
  }

  const header = `[global]
log_level = "info"

[mode]

[mode.clients]
enabled = true
refresh = true
misbehaviour = true

[mode.connections]
enabled = true

[mode.channels]
enabled = true

[mode.packets]
enabled = true
clear_interval = ${hermesClearInterval}
clear_on_start = ${hermesClearOnStart}
tx_confirmation = true

[rest]
enabled = false
host = "127.0.0.1"
port = 3000

[telemetry]
enabled = false
host = "127.0.0.1"
port = 3001
`;

  return `${header}
${chainList.map((chain) => chainConfig(chain, byChain.get(chain.chainId), keyName)).join("\n")}`;
}

function chainConfig(chain, allowedChannels, keyName) {
  const filter = allowedChannels
    ? `packet_filter = { policy = "allow", list = [${[...allowedChannels].sort().map((channel) => `["crossref", "${channel}"]`).join(", ")}] }\n`
    : "";
  return `[[chains]]
id = "${chain.chainId}"
type = "CosmosSdk"
rpc_addr = "http://${chain.service}:26657"
grpc_addr = "http://${chain.service}:9090"
event_source = { mode = "push", url = "ws://${chain.service}:26657/websocket", batch_delay = "500ms" }
rpc_timeout = "10s"
account_prefix = "crossref"
key_name = "${keyName}"
store_prefix = "ibc"
default_gas = 100000
max_gas = 400000
gas_price = { price = 0.0, denom = "stake" }
gas_multiplier = 1.2
max_msg_num = ${hermesMaxMsgNum}
max_tx_size = 2097152
sequential_batch_tx = ${hermesSequentialBatchTx}
clock_drift = "5s"
max_block_time = "10s"
trusting_period = "336hours"
trust_threshold = { numerator = "1", denominator = "3" }
memo_prefix = "crossref"
${filter}`;
}

function compose(topology, stem) {
  const lines = ["services:"];
  for (const chain of topology.chains) {
    lines.push(`  ${chain.service}:`);
    lines.push("    build:");
    lines.push("      context: ../..");
    lines.push("      dockerfile: docker/crossrefd.Dockerfile");
    lines.push(`    container_name: crossref-${stem}-${chain.service}`);
    lines.push("    environment:");
    lines.push(`      CHAIN_ID: ${chain.chainId}`);
    lines.push(`      MONIKER: ${chain.service}`);
    lines.push("      HOME_DIR: /var/crossref");
    lines.push(`      VALIDATOR_MNEMONIC: "${mnemonic}"`);
    lines.push(`      RELAYER_MNEMONIC: "${relayerMnemonic}"`);
    lines.push(`      RELAYER_WORKER_COUNT: "${topology.relayerCount}"`);
    lines.push('      CROSSREF_MAX_CHECKPOINT_PROOF_LAG: "${CROSSREF_MAX_CHECKPOINT_PROOF_LAG:-10000}"');
    lines.push('      CROSSREF_REQUIRE_CONSENSUS_PROOF: "${CROSSREF_REQUIRE_CONSENSUS_PROOF:-false}"');
    lines.push('    command: ["/opt/crossref/scripts/start-chain.sh"]');
    lines.push("    ports:");
    lines.push(`      - "${chain.rpcPort}:26657"`);
    lines.push(`      - "${chain.grpcPort}:9090"`);
    lines.push(`      - "${chain.apiPort}:1317"`);
    lines.push("    volumes:");
    lines.push(`      - ${chain.service}-data:/var/crossref`);
    lines.push("    healthcheck:");
    lines.push(`      test: ["CMD-SHELL", "curl -sf http://localhost:26657/status | grep -Eq '\\"latest_block_height\\":\\"[1-9][0-9]*\\"'"]`);
    lines.push("      interval: 5s");
    lines.push("      timeout: 3s");
    lines.push("      retries: 30");
    lines.push("");
  }
  lines.push("  relayer-init:");
  lines.push("    image: ghcr.io/informalsystems/hermes:1.10.5");
  lines.push(`    container_name: crossref-hermes-init-${stem}`);
  lines.push("    user: root");
  lines.push("    depends_on:");
  for (const chain of topology.chains) {
    lines.push(`      ${chain.service}:`);
    lines.push("        condition: service_healthy");
  }
  lines.push("    environment:");
  lines.push(`      RELAYER_MNEMONIC: "${relayerMnemonic}"`);
  lines.push("      HERMES_CONFIG: /root/.hermes/config.toml");
  lines.push("      RELAYER_MODE: init");
  lines.push(`      CROSSREF_CHAIN_IDS: "${topology.chains.map((c) => c.chainId).join(" ")}"`);
  lines.push(`      CROSSREF_CHANNEL_PAIRS: "${topology.channelPairs.join(" ")}"`);
  lines.push('    entrypoint: ["/bin/sh", "/opt/crossref/scripts/setup-ibc.sh"]');
  lines.push("    volumes:");
  lines.push(`      - ./hermes-${stem}.toml:/root/.hermes/config.toml:ro`);
  lines.push("      - ../scripts:/opt/crossref/scripts:ro");
  lines.push("");
  for (let worker = 1; worker <= topology.relayerCount; worker++) {
    lines.push(`  relayer-${worker}:`);
    lines.push("    image: ghcr.io/informalsystems/hermes:1.10.5");
    lines.push("    user: root");
    lines.push("    depends_on:");
    lines.push("      relayer-init:");
    lines.push("        condition: service_completed_successfully");
    lines.push("    environment:");
    lines.push(`      RELAYER_MNEMONIC: "${relayerMnemonic}"`);
    lines.push("      HERMES_CONFIG: /root/.hermes/config.toml");
    lines.push("      RELAYER_MODE: start");
    lines.push(`      RELAYER_INDEX: "${worker}"`);
    lines.push(`      RELAYER_WORKER_COUNT: "${topology.relayerCount}"`);
    lines.push(`      CROSSREF_CHAIN_IDS: "${topology.chains.map((c) => c.chainId).join(" ")}"`);
    lines.push('    entrypoint: ["/bin/sh", "/opt/crossref/scripts/setup-ibc.sh"]');
    lines.push("    volumes:");
    lines.push(`      - ./hermes-${stem}-worker-${worker}.toml:/root/.hermes/config.toml:ro`);
    lines.push("      - ../scripts:/opt/crossref/scripts:ro");
    lines.push("");
  }
  lines.push("volumes:");
  for (const chain of topology.chains) {
    lines.push(`  ${chain.service}-data:`);
  }
  return `${lines.join("\n")}\n`;
}

function suffixFor(index) {
  const alphabet = "abcdefghijklmnopqrstuvwxyz";
  if (index < alphabet.length) return alphabet[index];
  return `n${index + 1}`;
}

function positiveInt(value, name) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive integer`);
  }
  return parsed;
}

function nonNegativeInt(value, name) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error(`${name} must be a non-negative integer`);
  }
  return parsed;
}

function enumValue(value, name, allowed) {
  if (!allowed.includes(value)) {
    throw new Error(`${name} must be one of: ${allowed.join(", ")}`);
  }
  return value;
}

function boolValue(value, name) {
  if (value === "true") return true;
  if (value === "false") return false;
  throw new Error(`${name} must be true or false`);
}
