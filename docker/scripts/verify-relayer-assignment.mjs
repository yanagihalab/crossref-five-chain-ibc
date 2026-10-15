#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const topologyFile = process.env.TOPOLOGY_FILE ?? process.argv[2];
const configPattern = process.env.HERMES_CONFIG_PATTERN ?? process.argv[3];

if (!topologyFile || !configPattern) {
  console.error("usage: node docker/scripts/verify-relayer-assignment.mjs <topology.json> <config-pattern-with-{worker}>");
  process.exit(2);
}

const topology = JSON.parse(fs.readFileSync(topologyFile, "utf8"));
const activeWorkers = topology.activeRelayerWorkers ?? Array.from({ length: topology.relayerCount }, (_, index) => index + 1);
const requireNonEmptyWorkers = process.env.STRICT_NONEMPTY_WORKERS === "1";
const failures = [];
const observed = {};
const idleWorkers = [];
const channelOwners = new Map();

for (const worker of activeWorkers) {
  const configFile = configPattern.replaceAll("{worker}", String(worker));
  const config = fs.readFileSync(configFile, "utf8");
  observed[worker] = {
    config: path.relative(process.cwd(), configFile),
    routes: topology.routes.filter((route) => route.relayerWorker === worker).map((route) => route.id),
  };
  for (const route of topology.routes.filter((candidate) => candidate.relayerWorker === worker)) {
    if (!chainHasPacketFilter(config, route.fromChainId, route.fromChannel)) {
      failures.push(`${route.id} missing source channel ${route.from}/${route.fromChannel} in worker ${worker}`);
    }
    if (!chainHasPacketFilter(config, route.toChainId, route.toChannel)) {
      failures.push(`${route.id} missing destination channel ${route.to}/${route.toChannel} in worker ${worker}`);
    }
  }

  for (const [chainId, channels] of packetFiltersByChain(config)) {
    for (const channel of channels) {
      const key = `${chainId}/${channel}`;
      const owners = channelOwners.get(key) ?? [];
      owners.push(worker);
      channelOwners.set(key, owners);
    }
  }
}

for (const worker of activeWorkers) {
  if (observed[worker].routes.length === 0) {
    idleWorkers.push(worker);
    if (requireNonEmptyWorkers) {
      failures.push(`worker ${worker} has no assigned routes`);
    }
  }
}

for (const [channel, owners] of channelOwners) {
  const uniqueOwners = [...new Set(owners)];
  if (uniqueOwners.length > 1) {
    failures.push(`${channel} is allowed by multiple workers: ${uniqueOwners.join(", ")}`);
  }
}

if (failures.length) {
  console.error(JSON.stringify({ ok: false, failures, observed }, null, 2));
  process.exit(1);
}

console.log(JSON.stringify({
  ok: true,
  topology: path.relative(process.cwd(), topologyFile),
  workers: activeWorkers.length,
  idleWorkers,
  routes: topology.routes.length,
  observed,
}, null, 2));

function chainHasPacketFilter(config, chainId, channel) {
  const filters = packetFiltersByChain(config);
  return filters.get(chainId)?.has(channel) ?? false;
}

function packetFiltersByChain(config) {
  const filters = new Map();
  let currentChain = "";
  for (const line of config.split(/\n/)) {
    const id = line.match(/^id = "([^"]+)"/);
    if (id) {
      currentChain = id[1];
      continue;
    }
    const filter = line.match(/^packet_filter = .*list = \[(.*)\]/);
    if (filter && currentChain) {
      const channels = [...filter[1].matchAll(/\["crossref", "([^"]+)"\]/g)].map((match) => match[1]);
      filters.set(currentChain, new Set(channels));
    }
  }
  return filters;
}
