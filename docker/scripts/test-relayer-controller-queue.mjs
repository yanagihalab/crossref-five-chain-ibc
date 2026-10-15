#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "../..");
const outDir = path.join(root, "docker/generated/controller-queue-test");
fs.mkdirSync(outDir, { recursive: true });

run("node", ["docker/scripts/generate-topology.mjs", "3", "2"]);

const topologyFile = "docker/generated/topology-3c-2r.json";
const composeFile = "docker/generated/docker-compose-3c-2r.yml";

const basic = controller("basic", {
  CONTROLLER_DRY_RUN_DELAY_MS: "40",
});
assert.equal(basic.route_count, 6, "3-chain topology must produce 6 directed routes");
assert.equal(basic.delivered_routes, 6, "basic dry-run must deliver every route");
assert.equal(basic.failed_routes, 0, "basic dry-run must not fail");
assert.equal(basic.skipped_routes, 0, "basic dry-run must not skip routes");
assert.equal(basic.ibc_ack_event_count, 6, "dry-run should expose one IBC ACK per delivered route");
assert.equal(basic.crossref_ack_event_count, 6, "dry-run should expose one Crossref ACK per delivered route");
assertSerialPerQueue(basic);
assertParallelAcrossQueues(basic, 40);

const failover = controller("failover", {
  DISABLED_WORKERS: "1",
  CONTROLLER_DRY_RUN_DELAY_MS: "1",
});
assert.equal(failover.delivered_routes, 6, "failover dry-run must deliver every route");
assert.equal(failover.failed_routes, 0, "failover dry-run must not fail");
assert.deepEqual(failover.unhealthy_workers, [1], "worker 1 must be marked unhealthy");
assert.equal(failover.routes.filter((route) => route.reassigned === 1).length, 4, "worker 1 routes must be reassigned to worker 2");
assert.ok(failover.routes.filter((route) => route.reassigned === 1).every((route) => route.assigned_worker === 2), "reassigned routes should move to the remaining healthy worker");

const retry = controller("retry", {
  CONTROLLER_DRY_RUN_FAIL_ROUTES: "route-02",
  CONTROLLER_DRY_RUN_FAIL_ATTEMPTS: "2",
  CONTROLLER_MAX_RETRIES: "2",
  CONTROLLER_RETRY_BACKOFF_MS: "1",
});
assert.equal(retry.delivered_routes, 6, "retry case must eventually deliver every route");
assert.equal(retry.failed_routes, 0, "retry case must not leave failed routes");
assert.equal(retry.retry_count, 2, "retry count must include injected failures");
assert.equal(retry.routes.find((route) => route.route_id === "route-02")?.retries, 2, "route-02 must report two retries");

const feeFile = path.join(outDir, "fees.json");
fs.writeFileSync(feeFile, `${JSON.stringify({ "route-02": 2 }, null, 2)}\n`);
const fee = controller("fee", {
  CONTROLLER_ROUTE_FEE_FILE: feeFile,
  CONTROLLER_FEE_MODE: "enforce",
  CONTROLLER_MIN_PACKET_FEE: "1",
});
assert.equal(fee.delivered_routes, 1, "fee enforcement should relay only routes above the threshold");
assert.equal(fee.skipped_routes, 5, "fee enforcement should skip routes below the threshold");
assert.equal(fee.failed_routes, 0, "fee enforcement should not mark skipped routes as failed");
assert.equal(fee.routes.find((route) => route.route_id === "route-02")?.status, "success", "route-02 should be the delivered paid route");

const keyFile = path.join(outDir, "key-rotation.json");
fs.writeFileSync(keyFile, `${JSON.stringify({ workers: { "2": { keyName: "relayer-worker-2-rotated", hdAccountIndex: 22, keyEpoch: 3 } } }, null, 2)}\n`);
const keyRotation = controller("key-rotation", {
  CONTROLLER_KEY_ROTATION_FILE: keyFile,
});
const worker2Routes = keyRotation.routes.filter((route) => route.assigned_worker === 2);
assert.ok(worker2Routes.length > 0, "topology should include worker 2 routes");
assert.ok(worker2Routes.every((route) => route.key_name === "relayer-worker-2-rotated"), "worker 2 routes must use the rotated key name");
assert.ok(worker2Routes.every((route) => route.key_epoch === 3), "worker 2 routes must carry the rotated key epoch");

console.log(JSON.stringify({
  ok: true,
  tested: ["basic", "serial-queue", "parallel-queues", "failover", "retry", "fee-enforcement", "key-rotation"],
  out_dir: path.relative(root, outDir),
}, null, 2));

function controller(name, extraEnv = {}) {
  const metricsFile = path.join(outDir, `${name}.jsonl`);
  const summaryFile = path.join(outDir, `${name}.summary.json`);
  const alertsFile = path.join(outDir, `${name}.alerts.jsonl`);
  run("node", ["docker/scripts/relayer-controller.mjs", "--once", "--queue-scope", "account"], {
    DRY_RUN: "1",
    TOPOLOGY_FILE: topologyFile,
    COMPOSE_FILE: composeFile,
    RELAYER_COUNT: "2",
    CONTROLLER_METRICS_FILE: metricsFile,
    CONTROLLER_SUMMARY_FILE: summaryFile,
    CONTROLLER_ALERTS_FILE: alertsFile,
    CONTROLLER_MAX_RETRIES: "0",
    ...extraEnv,
  });
  return JSON.parse(fs.readFileSync(summaryFile, "utf8"));
}

function assertSerialPerQueue(summary) {
  const byQueue = new Map();
  for (const route of summary.routes.filter((item) => item.status === "success")) {
    assert.ok(Number.isFinite(route.started_time_ms), `${route.route_id} must include started_time_ms`);
    assert.ok(Number.isFinite(route.finished_time_ms), `${route.route_id} must include finished_time_ms`);
    if (!byQueue.has(route.queue_key)) byQueue.set(route.queue_key, []);
    byQueue.get(route.queue_key).push(route);
  }
  for (const [queueKey, routes] of byQueue) {
    routes.sort((a, b) => a.started_time_ms - b.started_time_ms);
    for (let i = 1; i < routes.length; i += 1) {
      assert.ok(
        routes[i].started_time_ms >= routes[i - 1].finished_time_ms,
        `${queueKey} must serialize ${routes[i - 1].route_id} before ${routes[i].route_id}`,
      );
    }
  }
}

function assertParallelAcrossQueues(summary, delayMs) {
  const starts = summary.routes.map((route) => route.started_time_ms).filter(Number.isFinite);
  const finishes = summary.routes.map((route) => route.finished_time_ms).filter(Number.isFinite);
  const elapsed = Math.max(...finishes) - Math.min(...starts);
  assert.ok(elapsed < summary.routes.length * delayMs, `queues should run in parallel, elapsed=${elapsed}ms`);
}

function run(command, args, extraEnv = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    env: { ...process.env, ...extraEnv },
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.status !== 0) {
    throw new Error([
      `${command} ${args.join(" ")} failed with ${result.status}`,
      result.stdout,
      result.stderr,
    ].filter(Boolean).join("\n"));
  }
  return result;
}
