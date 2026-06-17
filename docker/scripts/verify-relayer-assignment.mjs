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

for (const worker of activeWorkers) {
  const configFile = configPattern.replaceAll("{worker}", String(worker));
  const config = fs.readFileSync(configFile, "utf8");
  observed[worker] = {
    config: path.relative(process.cwd(), configFile),
    routes: topology.routes.filter((route) => route.relayerWorker === worker).map((route) => route.id),
  };
  for (const route of topology.routes.filter((candidate) => candidate.relayerWorker === worker)) {
    if (!config.includes(`["crossref", "${route.fromChannel}"]`)) {
      failures.push(`${route.id} missing source channel ${route.from}/${route.fromChannel} in worker ${worker}`);
    }
    if (!config.includes(`["crossref", "${route.toChannel}"]`)) {
      failures.push(`${route.id} missing destination channel ${route.to}/${route.toChannel} in worker ${worker}`);
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
