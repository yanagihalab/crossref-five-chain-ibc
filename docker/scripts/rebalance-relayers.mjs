#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { groupRoutesByChannelPair } from "./relayer-route-groups.mjs";

const root = process.cwd();
const topologyFile = required(process.env.TOPOLOGY_FILE ?? process.argv[2], "TOPOLOGY_FILE or argv[2]");
const relayerCount = positiveInt(process.env.RELAYER_COUNT ?? process.argv[3] ?? "1", "RELAYER_COUNT");
const disabled = new Set(splitList(process.env.DISABLED_WORKERS ?? ""));
const demandFile = process.env.DEMAND_FILE ?? "";
const hermesMaxMsgNum = positiveInt(process.env.HERMES_MAX_MSG_NUM ?? "1", "HERMES_MAX_MSG_NUM");
const hermesSequentialBatchTx = boolValue(process.env.HERMES_SEQUENTIAL_BATCH_TX ?? "true", "HERMES_SEQUENTIAL_BATCH_TX");
const hermesClearInterval = nonNegativeInt(process.env.HERMES_CLEAR_INTERVAL ?? "100", "HERMES_CLEAR_INTERVAL");
const hermesClearOnStart = boolValue(process.env.HERMES_CLEAR_ON_START ?? "true", "HERMES_CLEAR_ON_START");
const outDir = path.join(root, "docker", "generated");

const topology = JSON.parse(fs.readFileSync(topologyFile, "utf8"));
const demand = demandFile ? JSON.parse(fs.readFileSync(demandFile, "utf8")) : {};
const workers = Array.from({ length: relayerCount }, (_, index) => index + 1).filter((worker) => !disabled.has(String(worker)));

if (workers.length === 0) {
  throw new Error("no active relayer workers remain after DISABLED_WORKERS filtering");
}

const load = new Map(workers.map((worker) => [worker, 0]));
// Hermes filters own both ends of a channel, so opposite routes must stay together.
const sortedGroups = groupRoutesByChannelPair(topology.routes)
  .map((routes) => ({ routes, weight: routes.reduce((sum, route) => sum + routeWeight(route), 0) }))
  .sort((a, b) => b.weight - a.weight || a.routes[0].id.localeCompare(b.routes[0].id));
const assigned = new Map();

for (const group of sortedGroups) {
  const worker = workers.reduce((best, candidate) => {
    const bestLoad = load.get(best);
    const candidateLoad = load.get(candidate);
    return candidateLoad < bestLoad || (candidateLoad === bestLoad && candidate < best) ? candidate : best;
  }, workers[0]);
  for (const route of group.routes) assigned.set(route.id, worker);
  load.set(worker, load.get(worker) + group.weight);
}

topology.relayerCount = relayerCount;
topology.activeRelayerWorkers = workers;
topology.disabledRelayerWorkers = [...disabled].map(Number).filter(Number.isFinite).sort((a, b) => a - b);
topology.rebalance = {
  demandFile: demandFile || null,
  loads: Object.fromEntries([...load.entries()].map(([worker, value]) => [worker, value])),
};
topology.routes = topology.routes.map((route) => ({
  ...route,
  relayerWorker: assigned.get(route.id),
  demandWeight: routeWeight(route),
}));
if (topology.pairs) {
  const routesById = new Map(topology.routes.map((route) => [route.id, route]));
  topology.pairs = topology.pairs.map((pair) => ({
    ...pair,
    routes: pair.routes.map((route) => routesById.get(route.id)),
  }));
}

fs.mkdirSync(outDir, { recursive: true });
const stem = `${topology.chainCount}c-${relayerCount}r`;
const suffix = disabled.size ? `failover-${[...disabled].join("-")}` : "rebalance";
const topologyOut = path.join(outDir, `topology-${stem}-${suffix}.json`);
fs.writeFileSync(topologyOut, `${JSON.stringify(topology, null, 2)}\n`);
for (const worker of workers) {
  const routes = topology.routes.filter((route) => route.relayerWorker === worker);
  fs.writeFileSync(
    path.join(outDir, `hermes-${stem}-${suffix}-worker-${worker}.toml`),
    hermesConfig(topology.chains, routes, `relayer-worker-${worker}`),
  );
}
const overrideOut = path.join(outDir, `docker-compose-${stem}-${suffix}.override.yml`);
fs.writeFileSync(overrideOut, composeOverride(stem, suffix, workers));

console.log(JSON.stringify({
  topology: path.relative(root, topologyOut),
  composeOverride: path.relative(root, overrideOut),
  activeWorkers: workers,
  disabledWorkers: topology.disabledRelayerWorkers,
  routeCount: topology.routes.length,
  loads: topology.rebalance.loads,
}, null, 2));

function routeWeight(route) {
  const routeDemand = demand.routes?.[route.id] ?? demand.routes?.[`${route.from}->${route.to}`];
  if (routeDemand !== undefined) return positiveNumber(routeDemand, `demand ${route.id}`);
  const sourceDemand = demand.domains?.[route.from];
  if (sourceDemand !== undefined) return positiveNumber(sourceDemand, `demand ${route.from}`);
  return 1;
}

function hermesConfig(chainList, allowedRoutes, keyName) {
  const byChain = new Map(chainList.map((chain) => [chain.chainId, new Set()]));
  for (const route of allowedRoutes) {
    byChain.get(route.fromChainId)?.add(route.fromChannel);
    byChain.get(route.toChainId)?.add(route.toChannel);
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
  const list = [...allowedChannels].sort().map((channel) => `["crossref", "${channel}"]`).join(", ");
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
packet_filter = { policy = "allow", list = [${list}] }
`;
}

function composeOverride(stem, suffix, workers) {
  const lines = ["services:"];
  for (const worker of workers) {
    lines.push(`  relayer-${worker}:`);
    lines.push("    volumes:");
    lines.push(`      - ./hermes-${stem}-${suffix}-worker-${worker}.toml:/root/.hermes/config.toml:ro`);
    lines.push("      - ../scripts:/opt/crossref/scripts:ro");
  }
  return `${lines.join("\n")}\n`;
}

function splitList(value) {
  return value.split(/[,\s]+/).map((item) => item.trim()).filter(Boolean);
}

function required(value, name) {
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function positiveInt(value, name) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) throw new Error(`${name} must be a positive integer`);
  return parsed;
}

function positiveNumber(value, name) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) throw new Error(`${name} must be positive`);
  return parsed;
}

function nonNegativeInt(value, name) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error(`${name} must be a non-negative integer`);
  }
  return parsed;
}

function boolValue(value, name) {
  if (value === "true") return true;
  if (value === "false") return false;
  throw new Error(`${name} must be true or false`);
}
