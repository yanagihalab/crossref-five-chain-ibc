#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const chainCount = positiveInt(process.env.CHAIN_COUNT ?? process.argv[2] ?? "5", "CHAIN_COUNT");
const relayerCount = positiveInt(process.env.RELAYER_COUNT ?? process.argv[3] ?? "1", "RELAYER_COUNT");
const outDir = path.join(root, "docker", "generated");

if (chainCount < 2) {
  throw new Error("CHAIN_COUNT must be at least 2");
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
    rpcPort: 26657 + index * 10,
    grpcPort: 9090 + index,
    apiPort: 1317 + index,
  };
});

const pairs = [];
const channelCounters = Object.fromEntries(chains.map((chain) => [chain.domain, 0]));
let routeIndex = 0;
for (let i = 0; i < chains.length; i++) {
  for (let j = i + 1; j < chains.length; j++) {
    const left = chains[i];
    const right = chains[j];
    const leftChannel = `channel-${channelCounters[left.domain]++}`;
    const rightChannel = `channel-${channelCounters[right.domain]++}`;
    pairs.push({
      left: left.domain,
      right: right.domain,
      leftChainId: left.chainId,
      rightChainId: right.chainId,
      leftChannel,
      rightChannel,
      routes: [
        route(left, right, leftChannel, rightChannel, routeIndex++, relayerCount),
        route(right, left, rightChannel, leftChannel, routeIndex++, relayerCount),
      ],
    });
  }
}

const routes = pairs.flatMap((pair) => pair.routes);
const topology = {
  chainCount,
  relayerCount,
  chains,
  pairs,
  routes,
  channelPairs: pairs.map((pair) => `${pair.leftChainId}:${pair.rightChainId}`),
};

const stem = `${chainCount}c-${relayerCount}r`;
fs.writeFileSync(path.join(outDir, `topology-${stem}.json`), `${JSON.stringify(topology, null, 2)}\n`);
fs.writeFileSync(path.join(outDir, `hermes-${stem}.toml`), hermesConfig(chains));
for (let worker = 1; worker <= relayerCount; worker++) {
  fs.writeFileSync(path.join(outDir, `hermes-${stem}-worker-${worker}.toml`), hermesConfig(chains, routes.filter((r) => r.relayerWorker === worker)));
}
fs.writeFileSync(path.join(outDir, `docker-compose-${stem}.yml`), compose(topology, stem));

console.log(JSON.stringify({
  topology: path.relative(root, path.join(outDir, `topology-${stem}.json`)),
  compose: path.relative(root, path.join(outDir, `docker-compose-${stem}.yml`)),
  hermes: path.relative(root, path.join(outDir, `hermes-${stem}.toml`)),
  chainCount,
  relayerCount,
  routeCount: routes.length,
}, null, 2));

function route(from, to, fromChannel, toChannel, index, relayerCount) {
  return {
    id: `route-${String(index).padStart(2, "0")}`,
    from: from.domain,
    to: to.domain,
    fromChainId: from.chainId,
    toChainId: to.chainId,
    fromChannel,
    toChannel,
    relayerWorker: (index % relayerCount) + 1,
  };
}

function hermesConfig(chainList, allowedRoutes = null) {
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
clear_interval = 100
clear_on_start = true
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
${chainList.map((chain) => chainConfig(chain, byChain.get(chain.chainId))).join("\n")}`;
}

function chainConfig(chain, allowedChannels) {
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
key_name = "relayer"
store_prefix = "ibc"
default_gas = 100000
max_gas = 400000
gas_price = { price = 0.0, denom = "stake" }
gas_multiplier = 1.2
max_msg_num = 30
max_tx_size = 2097152
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
    lines.push("      context: ..");
    lines.push("      dockerfile: docker/crossrefd.Dockerfile");
    lines.push(`    container_name: crossref-${chain.service}`);
    lines.push("    environment:");
    lines.push(`      CHAIN_ID: ${chain.chainId}`);
    lines.push(`      MONIKER: ${chain.service}`);
    lines.push("      HOME_DIR: /var/crossref");
    lines.push(`      VALIDATOR_MNEMONIC: "${mnemonic}"`);
    lines.push(`      RELAYER_MNEMONIC: "${relayerMnemonic}"`);
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
  lines.push(`      - ./generated/hermes-${stem}.toml:/root/.hermes/config.toml:ro`);
  lines.push("      - ./scripts:/opt/crossref/scripts:ro");
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
    lines.push(`      - ./generated/hermes-${stem}-worker-${worker}.toml:/root/.hermes/config.toml:ro`);
    lines.push("      - ./scripts:/opt/crossref/scripts:ro");
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
