#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

const root = process.cwd();
const topologyFile = required(process.env.TOPOLOGY_FILE ?? process.argv[2], "TOPOLOGY_FILE or argv[2]");
const relayerCount = positiveInt(process.env.RELAYER_COUNT ?? process.argv[3] ?? "1", "RELAYER_COUNT");
const composeFile = process.env.COMPOSE_FILE ?? "";
const intervalMs = positiveInt(process.env.CONTROLLER_INTERVAL_MS ?? "15000", "CONTROLLER_INTERVAL_MS");
const once = flag("CONTROLLER_ONCE") || process.argv.includes("--once");
const dryRun = flag("DRY_RUN") || process.argv.includes("--dry-run");
const outDir = path.join(root, "docker", "generated");
const metricsFile = process.env.METRICS_FILE ?? path.join(outDir, "relayer-controller-metrics.jsonl");
const alertsFile = process.env.ALERTS_FILE ?? path.join(outDir, "relayer-controller-alerts.jsonl");

fs.mkdirSync(outDir, { recursive: true });

do {
  const topology = JSON.parse(fs.readFileSync(topologyFile, "utf8"));
  const health = collectHealth(relayerCount);
  const disabledWorkers = health.filter((worker) => !worker.healthy).map((worker) => worker.worker);
  const rebalance = rebalanceRoutes(topologyFile, relayerCount, disabledWorkers);
  const metric = {
    ts: new Date().toISOString(),
    relayerCount,
    routeCount: topology.routes.length,
    healthyWorkers: health.filter((worker) => worker.healthy).map((worker) => worker.worker),
    disabledWorkers,
    rebalance,
  };
  appendJSON(metricsFile, metric);
  if (disabledWorkers.length > 0) {
    appendJSON(alertsFile, {
      ts: metric.ts,
      severity: "warning",
      type: "relayer_failover",
      disabledWorkers,
      action: "routes_rebalanced",
      composeOverride: rebalance.composeOverride,
    });
    applyComposeOverride(rebalance.composeOverride);
  }
  if (once) break;
  await sleep(intervalMs);
} while (true);

function collectHealth(count) {
  return Array.from({ length: count }, (_, index) => {
    const worker = index + 1;
    const service = `relayer-${worker}`;
    if (dryRun || !composeFile) {
      return { worker, service, healthy: true, reason: dryRun ? "dry_run" : "no_compose_file" };
    }
    const result = spawnSync("docker", ["compose", "-f", composeFile, "ps", "--format", "json", service], {
      cwd: root,
      encoding: "utf8",
    });
    if (result.status !== 0) {
      return { worker, service, healthy: false, reason: result.stderr.trim() || "compose_ps_failed" };
    }
    const lines = result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
    if (lines.length === 0) {
      return { worker, service, healthy: false, reason: "service_missing" };
    }
    const rows = lines.map((line) => JSON.parse(line));
    const healthy = rows.some((row) => {
      const state = String(row.State ?? row.state ?? "").toLowerCase();
      const health = String(row.Health ?? row.health ?? "").toLowerCase();
      return state === "running" && (health === "" || health === "healthy");
    });
    return { worker, service, healthy, reason: healthy ? "running" : "not_running_or_unhealthy" };
  });
}

function rebalanceRoutes(topology, count, disabledWorkers) {
  const env = {
    ...process.env,
    TOPOLOGY_FILE: topology,
    RELAYER_COUNT: String(count),
    DISABLED_WORKERS: disabledWorkers.join(","),
  };
  const result = spawnSync("node", ["docker/scripts/rebalance-relayers.mjs"], {
    cwd: root,
    env,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(result.stderr || result.stdout || "rebalance failed");
  }
  return JSON.parse(result.stdout);
}

function applyComposeOverride(overrideFile) {
  if (dryRun || !composeFile || !overrideFile) return;
  const overridePath = path.isAbsolute(overrideFile) ? overrideFile : path.join(root, overrideFile);
  const result = spawnSync("docker", ["compose", "-f", composeFile, "-f", overridePath, "up", "-d", "--no-deps"], {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    appendJSON(alertsFile, {
      ts: new Date().toISOString(),
      severity: "critical",
      type: "relayer_failover_apply_failed",
      error: result.stderr.trim() || result.stdout.trim(),
    });
  }
}

function appendJSON(file, value) {
  fs.appendFileSync(file, `${JSON.stringify(value)}\n`);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function flag(name) {
  return /^(1|true|yes)$/i.test(process.env[name] ?? "");
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
