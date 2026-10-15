#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const scripts = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scripts, "../..");
fs.mkdirSync(path.join(root, "docker/generated"), { recursive: true });
const output = fs.mkdtempSync(path.join(root, "docker/generated/multi-relayer-preflight-"));
const copiedScripts = path.join(output, "docker/scripts");
fs.mkdirSync(copiedScripts, { recursive: true });
for (const name of [
  "generate-topology.mjs", "rebalance-relayers.mjs", "verify-relayer-assignment.mjs",
  "relayer-controller.mjs", "test-relayer-controller-queue.mjs",
  "relayer-route-groups.mjs",
]) {
  fs.copyFileSync(path.join(scripts, name), path.join(copiedScripts, name));
}

// These checks exercise assignment and controller logic, not actual packet delivery.
const results = [];
check("existing-controller-queue-regressions", () => {
  success(run("test-relayer-controller-queue.mjs"));
});

for (const workers of [1, 2, 3]) {
  check(`static-${workers}-workers`, () => {
    const topology = generate(workers);
    verify(topology, `hermes-3c-${workers}r-worker-{worker}.toml`);
    const { summary, metrics } = controller(`static-${workers}`, topology, workers);
    assert.equal(summary.delivered_routes, 6);
    assert.equal(summary.failed_routes, 0);
    assert.equal(summary.healthy_workers.length, workers);
    assert.equal(new Set(summary.routes.map((route) => route.key_name)).size, workers);
    assert.equal(metrics.filter((item) => item.type === "controller_route_attempt").length, 6);
  });
}

const base = generate(3);
check("controller-live-failover-keeps-opposite-routes-together", () => {
  const summary = controller("controller-failover", base, 3, [1]).summary;
  assert.equal(summary.delivered_routes, 6);
  assert.ok(summary.routes.every((route) => route.assigned_worker !== 1));
  const owners = new Map(summary.routes.map((route) => [route.route_id, route.assigned_worker]));
  for (const pair of readJson(base).pairs) {
    assert.equal(new Set(pair.routes.map((route) => owners.get(route.id))).size, 1);
  }
});
for (const disabled of [[1], [2], [3], [1, 2]]) {
  check(`exclusive-channel-failover-${disabled.join("-")}`, () => {
    const topology = rebalance(base, disabled);
    const savedTopology = readJson(topology);
    assert.deepEqual(savedTopology.pairs.flatMap((pair) => pair.routes), savedTopology.routes);
    verify(topology, `hermes-3c-3r-failover-${disabled.join("-")}-worker-{worker}.toml`);
    const summary = controller(`failover-${disabled.join("-")}`, topology, 3, disabled).summary;
    assert.equal(summary.delivered_routes, 6);
    assert.ok(summary.routes.every((route) => !disabled.includes(route.assigned_worker)));
  });
}

check("all-workers-down-pauses-without-relay-attempts", () => {
  const result = controller("all-down", base, 3, [1, 2, 3]);
  assert.notEqual(result.process.status, 0, "no healthy workers must not report success");
  assert.deepEqual(result.summary.healthy_workers, []);
  assert.equal(result.summary.delivered_routes, 0);
  assert.equal(result.summary.failed_routes, 6);
  assert.equal(result.summary.blocked_routes, 6);
  assert.equal(result.metrics.filter((item) => item.type === "controller_route_attempt").length, 0);
});

check("all-workers-down-rebalance-refuses-assignment", () => {
  const result = run("rebalance-relayers.mjs", [], {
    TOPOLOGY_FILE: base, RELAYER_COUNT: "3", DISABLED_WORKERS: "1,2,3",
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /no active relayer workers/);
});

check("failed-route-returns-nonzero-exit-status", () => {
  const result = controller("failed-route", base, 3, [], {
    CONTROLLER_DRY_RUN_FAIL_ROUTES: "route-00", CONTROLLER_DRY_RUN_FAIL_ATTEMPTS: "1",
  });
  assert.notEqual(result.process.status, 0);
  assert.equal(result.summary.failed_routes, 1);
  assert.equal(result.summary.delivered_routes, 5);
});

check("worker-return-restores-valid-assignment", () => {
  const failover = rebalance(base, [1]);
  const restored = rebalance(failover, []);
  verify(restored, "hermes-3c-3r-rebalance-worker-{worker}.toml");
  const summary = controller("restored", restored, 3).summary;
  assert.equal(summary.delivered_routes, 6);
  assert.deepEqual(summary.healthy_workers, [1, 2, 3]);
  assert.deepEqual(new Set(summary.routes.map((route) => route.assigned_worker)), new Set([1, 2, 3]));
});

check("demand-weighted-rebalance-keeps-channel-ownership", () => {
  const demandFile = path.join(output, "demand.json");
  fs.writeFileSync(demandFile, `${JSON.stringify({ routes: { "route-00": 20, "route-01": 20 } })}\n`);
  const topologyFile = rebalance(base, [], { DEMAND_FILE: demandFile });
  verify(topologyFile, "hermes-3c-3r-rebalance-worker-{worker}.toml");
  const topology = readJson(topologyFile);
  const hotRoutes = topology.routes.filter((route) => ["route-00", "route-01"].includes(route.id));
  assert.equal(new Set(hotRoutes.map((route) => route.relayerWorker)).size, 1);
  assert.equal(Object.values(topology.rebalance.loads).reduce((sum, value) => sum + value, 0), 44);
});

check("directed-destination-sharding-survives-rebalance", () => {
  const topology = generate(3, { CHANNEL_MODE: "directed", RELAYER_SHARD_MODE: "destination" });
  const reassigned = rebalance(topology, [1]);
  verify(reassigned, "hermes-3c-3r-failover-1-worker-{worker}.toml");
  const summary = controller("directed-failover", reassigned, 3, [1]).summary;
  assert.equal(summary.delivered_routes, 6);
  assert.ok(summary.routes.every((route) => route.assigned_worker !== 1));
});

check("five-chain-pair-failover-keeps-channel-ownership", () => {
  const topology = generate(3, { CHAIN_COUNT: "5" });
  const reassigned = rebalance(topology, [1]);
  verify(reassigned, "hermes-5c-3r-failover-1-worker-{worker}.toml");
  const summary = controller("five-chain-failover", reassigned, 3, [1]).summary;
  assert.equal(summary.delivered_routes, 20);
  assert.ok(summary.routes.every((route) => route.assigned_worker !== 1));
});

const report = {
  schemaVersion: 1,
  generatedAt: new Date().toISOString(),
  dryRunOnly: true,
  actualPacketsSent: 0,
  vpsModified: false,
  ok: results.every((result) => result.ok),
  passed: results.filter((result) => result.ok).length,
  failed: results.filter((result) => !result.ok).length,
  results,
};
const reportPath = path.join(output, "preflight-report.json");
fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
console.log(JSON.stringify({ ...report, reportPath }, null, 2));
process.exitCode = report.ok ? 0 : 1;

function check(name, test) {
  try {
    test();
    results.push({ name, ok: true });
  } catch (error) {
    results.push({ name, ok: false, reason: error.message.slice(0, 1200) });
  }
}

function generate(workers, overrides = {}) {
  const result = success(run("generate-topology.mjs", ["3", String(workers)], {
    CHAIN_COUNT: "3", RELAYER_COUNT: String(workers), ...overrides,
  }));
  return path.join(output, JSON.parse(result.stdout).topology);
}

function rebalance(topology, disabled, overrides = {}) {
  const result = success(run("rebalance-relayers.mjs", [], {
    TOPOLOGY_FILE: topology, RELAYER_COUNT: "3", DISABLED_WORKERS: disabled.join(","), ...overrides,
  }));
  return path.join(output, JSON.parse(result.stdout).topology);
}

function verify(topology, configPattern) {
  success(run("verify-relayer-assignment.mjs", [
    topology, path.join(output, "docker/generated", configPattern),
  ]));
}

function controller(name, topology, workers, disabled = [], overrides = {}) {
  const summaryFile = path.join(output, `${name}.summary.json`);
  const metricsFile = path.join(output, `${name}.metrics.jsonl`);
  const result = run("relayer-controller.mjs", ["--once", "--queue-scope", "account"], {
    DRY_RUN: "1", TOPOLOGY_FILE: topology, RELAYER_COUNT: String(workers),
    DISABLED_WORKERS: disabled.join(","), CONTROLLER_MAX_RETRIES: "0",
    CONTROLLER_SUMMARY_FILE: summaryFile, CONTROLLER_METRICS_FILE: metricsFile,
    CONTROLLER_ALERTS_FILE: path.join(output, `${name}.alerts.jsonl`),
    ...overrides,
  });
  return {
    process: result,
    summary: readJson(summaryFile),
    metrics: fs.readFileSync(metricsFile, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse),
  };
}

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function run(script, args = [], overrides = {}) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) =>
    !/^(CONTROLLER_|RELAYER_|HERMES_)/.test(name) &&
    !["TOPOLOGY_FILE", "COMPOSE_FILE", "DRY_RUN", "DISABLED_WORKERS", "CHAIN_COUNT",
      "CHANNEL_MODE", "HOST_PORT_OFFSET", "DEMAND_FILE", "HERMES_CONFIG_PATTERN",
      "STRICT_NONEMPTY_WORKERS", "METRICS_FILE", "CROSSREF_RUN_ID"].includes(name),
  ));
  return spawnSync(process.execPath, [path.join(copiedScripts, script), ...args], {
    cwd: output, env: { ...env, ...overrides }, encoding: "utf8",
    timeout: 30000, maxBuffer: 4 * 1024 * 1024,
  });
}

function success(result) {
  assert.equal(result.status, 0, result.error?.message ?? result.stderr.trim());
  return result;
}
