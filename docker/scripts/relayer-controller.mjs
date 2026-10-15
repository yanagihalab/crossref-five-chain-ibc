#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { groupRoutesByChannelPair } from "./relayer-route-groups.mjs";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "../..");
const args = parseArgs(process.argv.slice(2));

const topologyFile = resolvePath(required(args.topology ?? process.env.TOPOLOGY_FILE, "TOPOLOGY_FILE or --topology"));
const composeFile = resolvePath(args.composeFile ?? process.env.COMPOSE_FILE ?? "");
const relayerCount = positiveInt(args.relayerCount ?? process.env.RELAYER_COUNT ?? process.env.RELAYER_WORKER_COUNT ?? "1", "RELAYER_COUNT");
const once = flag(args.once ?? process.env.CONTROLLER_ONCE) || !flag(args.serve ?? process.env.CONTROLLER_SERVE);
const dryRun = flag(args.dryRun ?? process.env.DRY_RUN);
const intervalMs = positiveInt(args.intervalMs ?? process.env.CONTROLLER_INTERVAL_MS ?? "15000", "CONTROLLER_INTERVAL_MS");
const healthMode = enumValue(args.healthMode ?? process.env.CONTROLLER_HEALTH_MODE ?? "config", "CONTROLLER_HEALTH_MODE", ["config", "running", "hermes"]);
const queueScope = enumValue(args.queueScope ?? process.env.CONTROLLER_QUEUE_SCOPE ?? "account", "CONTROLLER_QUEUE_SCOPE", ["account", "destination", "worker", "global"]);
const maxRetries = nonNegativeInt(args.maxRetries ?? process.env.CONTROLLER_MAX_RETRIES ?? "2", "CONTROLLER_MAX_RETRIES");
const retryBackoffMs = nonNegativeInt(args.retryBackoffMs ?? process.env.CONTROLLER_RETRY_BACKOFF_MS ?? "1500", "CONTROLLER_RETRY_BACKOFF_MS");
const commandTimeoutMs = positiveInt(args.commandTimeoutMs ?? process.env.CONTROLLER_COMMAND_TIMEOUT_MS ?? "90000", "CONTROLLER_COMMAND_TIMEOUT_MS");
const runId = args.runId ?? process.env.CROSSREF_RUN_ID ?? `relayer-controller-${Date.now()}`;
const metricsFile = resolvePath(args.metricsFile ?? process.env.CONTROLLER_METRICS_FILE ?? process.env.METRICS_FILE ?? "docker/generated/relayer-controller-metrics.jsonl");
const summaryFile = resolvePath(args.summaryFile ?? process.env.CONTROLLER_SUMMARY_FILE ?? "docker/generated/relayer-controller-summary.json");
const alertsFile = resolvePath(args.alertsFile ?? process.env.CONTROLLER_ALERTS_FILE ?? "docker/generated/relayer-controller-alerts.jsonl");
const keyRotationFile = resolvePath(args.keyRotationFile ?? process.env.CONTROLLER_KEY_ROTATION_FILE ?? "");
const routeFeeFile = resolvePath(args.routeFeeFile ?? process.env.CONTROLLER_ROUTE_FEE_FILE ?? "");
const minPacketFee = nonNegativeNumber(args.minPacketFee ?? process.env.CONTROLLER_MIN_PACKET_FEE ?? "0", "CONTROLLER_MIN_PACKET_FEE");
const feeMode = enumValue(args.feeMode ?? process.env.CONTROLLER_FEE_MODE ?? "metadata", "CONTROLLER_FEE_MODE", ["off", "metadata", "enforce"]);
const relayerImage = args.relayerImage ?? process.env.CONTROLLER_RELAYER_IMAGE ?? "ghcr.io/informalsystems/hermes:1.10.5";
const relayerMnemonic = process.env.RELAYER_MNEMONIC ?? "turtle all pyramid produce tray equip elder border rose risk slight panther grant price smile try fly street carpet wage among hole join suit";
const clearOnInterval = flag(args.clearOnInterval ?? process.env.CONTROLLER_CLEAR_ON_INTERVAL);
const disabledWorkers = new Set(splitList(args.disabledWorkers ?? process.env.DISABLED_WORKERS ?? process.env.CONTROLLER_DISABLED_WORKERS ?? "").map(Number));
const dryRunDelayMs = nonNegativeInt(args.dryRunDelayMs ?? process.env.CONTROLLER_DRY_RUN_DELAY_MS ?? "0", "CONTROLLER_DRY_RUN_DELAY_MS");
const dryRunFailRoutes = new Set(splitList(args.dryRunFailRoutes ?? process.env.CONTROLLER_DRY_RUN_FAIL_ROUTES ?? ""));
const dryRunFailAttempts = nonNegativeInt(args.dryRunFailAttempts ?? process.env.CONTROLLER_DRY_RUN_FAIL_ATTEMPTS ?? "0", "CONTROLLER_DRY_RUN_FAIL_ATTEMPTS");
const dryRunAttempts = new Map();

fs.mkdirSync(path.dirname(metricsFile), { recursive: true });
fs.mkdirSync(path.dirname(summaryFile), { recursive: true });
fs.mkdirSync(path.dirname(alertsFile), { recursive: true });

if (once) {
  const result = await controllerTick({ relayPackets: true });
  process.exit(result.failed_routes > 0 ? 1 : 0);
}

for (;;) {
  await controllerTick({ relayPackets: clearOnInterval });
  await sleep(intervalMs);
}

async function controllerTick({ relayPackets }) {
  const topology = JSON.parse(fs.readFileSync(topologyFile, "utf8"));
  const keyRotation = readOptionalJson(keyRotationFile) ?? {};
  const routeFees = readOptionalJson(routeFeeFile) ?? {};
  const health = collectHealth(topology);
  const healthyWorkers = health.filter((item) => item.healthy).map((item) => item.worker);
  const unhealthyWorkers = health.filter((item) => !item.healthy).map((item) => item.worker);
  const activeWorkers = healthyWorkers;
  const assignments = activeWorkers.length > 0 ? assignRoutes(topology.routes, activeWorkers, keyRotation, routeFees) : [];
  const blockedRoutes = activeWorkers.length === 0 ? topology.routes.length : 0;
  const summary = {
    run_id: runId,
    time_ms: Date.now(),
    ts: new Date().toISOString(),
    topology_file: path.relative(root, topologyFile),
    compose_file: composeFile ? path.relative(root, composeFile) : "",
    queue_scope: queueScope,
    health_mode: healthMode,
    relayer_count: relayerCount,
    route_count: topology.routes.length,
    relay_packets: relayPackets,
    healthy_workers: healthyWorkers,
    unhealthy_workers: unhealthyWorkers,
    disabled_workers: [...disabledWorkers].sort((a, b) => a - b),
    fee_mode: feeMode,
    min_packet_fee: minPacketFee,
    dry_run_delay_ms: dryRun ? dryRunDelayMs : 0,
    dry_run_fail_routes: dryRun ? [...dryRunFailRoutes].sort() : [],
    dry_run_fail_attempts: dryRun ? dryRunFailAttempts : 0,
    routes: [],
    worker_utilization: {},
    queue_utilization: {},
    delivered_routes: 0,
    failed_routes: relayPackets ? blockedRoutes : 0,
    blocked_routes: blockedRoutes,
    skipped_routes: 0,
    retry_count: 0,
    account_sequence_mismatch_count: 0,
    ibc_ack_event_count: 0,
    crossref_ack_event_count: 0,
  };

  appendMetric("controller_tick_start", {
    queue_scope: queueScope,
    health_mode: healthMode,
    relay_packets: relayPackets ? 1 : 0,
    healthy_workers: healthyWorkers.join(","),
    unhealthy_workers: unhealthyWorkers.join(","),
  });

  for (const worker of unhealthyWorkers) {
    appendAlert("warning", "relayer_worker_unhealthy", { worker, action: "route_reassignment_enabled" });
  }
  if (blockedRoutes > 0) {
    appendAlert("critical", "no_healthy_relayer_workers", { action: "relay_paused", blocked_routes: blockedRoutes });
  }

  if (relayPackets) {
    const results = await runQueues(assignments, topology);
    for (const result of results) {
      summary.routes.push(result);
      summary.worker_utilization[result.assigned_worker] ??= { routes: 0, successes: 0, failures: 0, skipped: 0, duration_ms: 0 };
      summary.queue_utilization[result.queue_key] ??= { routes: 0, successes: 0, failures: 0, skipped: 0, duration_ms: 0 };
      const workerBucket = summary.worker_utilization[result.assigned_worker];
      const queueBucket = summary.queue_utilization[result.queue_key];
      for (const bucket of [workerBucket, queueBucket]) {
        bucket.routes += 1;
        bucket.duration_ms += result.duration_ms ?? 0;
        if (result.status === "success") bucket.successes += 1;
        else if (result.status === "skipped") bucket.skipped += 1;
        else bucket.failures += 1;
      }
      if (result.status === "success") summary.delivered_routes += 1;
      else if (result.status === "skipped") summary.skipped_routes += 1;
      else summary.failed_routes += 1;
      summary.retry_count += result.retries ?? 0;
      summary.account_sequence_mismatch_count += result.account_sequence_mismatch_count ?? 0;
      summary.ibc_ack_event_count += result.ibc_ack_event_count ?? 0;
      summary.crossref_ack_event_count += result.crossref_ack_event_count ?? 0;
    }
  }

  fs.writeFileSync(summaryFile, `${JSON.stringify(summary, null, 2)}\n`);
  appendMetric("controller_tick_end", {
    delivered_routes: summary.delivered_routes,
    failed_routes: summary.failed_routes,
    skipped_routes: summary.skipped_routes,
    retry_count: summary.retry_count,
    account_sequence_mismatch_count: summary.account_sequence_mismatch_count,
    ibc_ack_event_count: summary.ibc_ack_event_count,
    crossref_ack_event_count: summary.crossref_ack_event_count,
  });
  console.log(JSON.stringify(summary, null, 2));
  return summary;
}

function collectHealth(topology) {
  const serviceSet = new Set();
  if (composeFile && !dryRun) {
    const result = spawnSync("docker", ["compose", "-f", composeFile, "config", "--services"], { cwd: root, encoding: "utf8" });
    if (result.status === 0) {
      for (const service of result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)) serviceSet.add(service);
    }
  }
  return Array.from({ length: relayerCount }, (_, index) => {
    const worker = index + 1;
    const service = `relayer-${worker}`;
    if (disabledWorkers.has(worker)) return { worker, service, healthy: false, reason: "disabled" };
    if (dryRun || !composeFile) return { worker, service, healthy: true, reason: dryRun ? "dry_run" : "no_compose_file" };
    if (serviceSet.size > 0 && !serviceSet.has(service)) return { worker, service, healthy: false, reason: "service_missing" };
    if (healthMode === "config") return { worker, service, healthy: true, reason: "service_configured" };
    const ps = spawnSync("docker", ["compose", "-f", composeFile, "ps", "--format", "json", service], { cwd: root, encoding: "utf8" });
    if (ps.status !== 0) return { worker, service, healthy: false, reason: ps.stderr.trim() || "compose_ps_failed" };
    const rows = ps.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).map((line) => JSON.parse(line));
    const running = rows.some((row) => String(row.State ?? row.state ?? "").toLowerCase() === "running");
    if (!running) return { worker, service, healthy: false, reason: "not_running" };
    if (healthMode === "running") return { worker, service, healthy: true, reason: "running" };
    const route = topology.routes.find((item) => Number(item.relayerWorker) === worker) ?? topology.routes[0];
    const command = hermesCommand(topology, route, worker, ["health-check"]);
    const result = runCommand(command, { timeout: commandTimeoutMs });
    return { worker, service, healthy: result.status === 0, reason: result.status === 0 ? "hermes_health_check" : "hermes_health_failed" };
  });
}

function assignRoutes(routes, activeWorkers, keyRotation, routeFees) {
  const active = activeWorkers;
  const load = new Map(active.map((worker) => [worker, 0]));
  const owners = new Map();
  for (const group of groupRoutesByChannelPair(routes)) {
    const originalWorker = Number(group[0].relayerWorker) || 1;
    const assignedWorker = active.includes(originalWorker) ? originalWorker : leastLoadedWorker(load);
    load.set(assignedWorker, load.get(assignedWorker) + group.length);
    for (const route of group) owners.set(route.id, assignedWorker);
  }
  return routes.map((route) => {
    const originalWorker = Number(route.relayerWorker) || 1;
    const assignedWorker = owners.get(route.id);
    const key = keyForWorker(assignedWorker, keyRotation);
    const fee = feeForRoute(route, routeFees);
    return {
      route,
      originalWorker,
      assignedWorker,
      key,
      fee,
      queueKey: queueKeyForRoute(route, assignedWorker, key),
      reassigned: assignedWorker !== originalWorker,
    };
  });
}

function leastLoadedWorker(load) {
  return [...load.entries()].sort((a, b) => a[1] - b[1] || a[0] - b[0])[0]?.[0] ?? 1;
}

function keyForWorker(worker, keyRotation) {
  const workers = keyRotation.workers ?? {};
  const entry = workers[String(worker)] ?? {};
  const epoch = Number(entry.keyEpoch ?? keyRotation.keyEpoch ?? process.env.CONTROLLER_KEY_EPOCH ?? 0);
  return {
    keyName: entry.keyName ?? `relayer-worker-${worker}`,
    hdAccountIndex: Number(entry.hdAccountIndex ?? worker),
    keyEpoch: Number.isFinite(epoch) ? epoch : 0,
  };
}

function feeForRoute(route, routeFees) {
  const value = routeFees[route.id] ?? routeFees[`${route.from}->${route.to}`] ?? route.fee ?? 0;
  const amount = Number(typeof value === "object" ? value.amount : value);
  return Number.isFinite(amount) ? amount : 0;
}

function queueKeyForRoute(route, worker, key) {
  if (queueScope === "global") return "global";
  if (queueScope === "worker") return `worker:${worker}`;
  if (queueScope === "destination") return `destination:${route.toChainId}`;
  return `account:${route.toChainId}:${key.keyName}`;
}

async function runQueues(assignments, topology) {
  const queues = new Map();
  for (const assignment of assignments) {
    if (feeMode === "enforce" && assignment.fee < minPacketFee) {
      appendMetric("controller_route_skipped", routeMetric(assignment, { reason: "fee_below_minimum" }));
      continue;
    }
    if (!queues.has(assignment.queueKey)) queues.set(assignment.queueKey, []);
    queues.get(assignment.queueKey).push(assignment);
  }
  const skipped = assignments
    .filter((assignment) => feeMode === "enforce" && assignment.fee < minPacketFee)
    .map((assignment) => ({
      ...routeMetric(assignment),
      status: "skipped",
      duration_ms: 0,
      retries: 0,
      account_sequence_mismatch_count: 0,
      ibc_ack_event_count: 0,
      crossref_ack_event_count: 0,
    }));
  const queueResults = await Promise.all([...queues.values()].map((queue) => runQueue(queue, topology)));
  return [...skipped, ...queueResults.flat()];
}

async function runQueue(queue, topology) {
  const results = [];
  for (const assignment of queue) {
    results.push(await relayWithRetry(assignment, topology));
  }
  return results;
}

async function relayWithRetry(assignment, topology) {
  const started = Date.now();
  appendMetric("controller_route_start", routeMetric(assignment, { fee: assignment.fee }));
  let attempt = 0;
  let last = null;
  while (attempt <= maxRetries) {
    const command = hermesCommand(topology, assignment.route, assignment.assignedWorker, [
      "clear",
      "packets",
      "--chain",
      assignment.route.fromChainId,
      "--port",
      "crossref",
      "--channel",
      assignment.route.fromChannel,
    ], assignment.key);
    if (dryRun) {
      last = await runDryCommand(assignment, command);
    } else {
      last = runCommand(command, { timeout: commandTimeoutMs });
    }
    const output = `${last.stdout}\n${last.stderr}`;
    const mismatchCount = countMatches(output, /account sequence mismatch/g);
    const ibcAckCount = countMatches(output, /AcknowledgePacket/g);
    const crossrefAckCount = countMatches(output, /WriteAcknowledgement|CrossReferencePacketAck|crossref_ack/g);
    appendMetric("controller_route_attempt", routeMetric(assignment, {
      attempt: attempt + 1,
      status_code: last.status,
      duration_ms: Date.now() - started,
      account_sequence_mismatch_count: mismatchCount,
      ibc_ack_event_count: ibcAckCount,
      crossref_ack_event_count: crossrefAckCount,
    }));
    if (last.status === 0) {
      const duration = Date.now() - started;
      const result = {
        ...routeMetric(assignment),
        status: "success",
        started_time_ms: started,
        finished_time_ms: started + duration,
        duration_ms: duration,
        retries: attempt,
        account_sequence_mismatch_count: mismatchCount,
        ibc_ack_event_count: ibcAckCount,
        crossref_ack_event_count: crossrefAckCount,
      };
      appendMetric("controller_route_success", result);
      return result;
    }
    attempt += 1;
    if (attempt <= maxRetries) await sleep(retryBackoffMs * attempt);
  }
  const output = `${last?.stdout ?? ""}\n${last?.stderr ?? ""}`;
  const result = {
    ...routeMetric(assignment),
    status: "failed",
    started_time_ms: started,
    finished_time_ms: Date.now(),
    duration_ms: Date.now() - started,
    retries: Math.max(0, attempt - 1),
    exit_code: last?.status ?? -1,
    account_sequence_mismatch_count: countMatches(output, /account sequence mismatch/g),
    ibc_ack_event_count: countMatches(output, /AcknowledgePacket/g),
    crossref_ack_event_count: countMatches(output, /WriteAcknowledgement|CrossReferencePacketAck|crossref_ack/g),
    error_tail: output.split(/\r?\n/).filter(Boolean).slice(-8).join("\n"),
  };
  appendMetric("controller_route_failed", result);
  appendAlert("critical", "controller_route_failed", result);
  return result;
}

async function runDryCommand(assignment, command) {
  if (dryRunDelayMs > 0) await sleep(dryRunDelayMs);
  const routeId = assignment.route.id;
  const attempt = (dryRunAttempts.get(routeId) ?? 0) + 1;
  dryRunAttempts.set(routeId, attempt);
  if (dryRunFailRoutes.has(routeId) && attempt <= dryRunFailAttempts) {
    return {
      status: 1,
      stdout: "",
      stderr: `dry-run injected failure for ${routeId} attempt ${attempt}`,
      command: command.args.join(" "),
    };
  }
  return {
    status: 0,
    stdout: "dry-run relay success\nAcknowledgePacket\nCrossReferencePacketAck\n",
    stderr: "",
    command: command.args.join(" "),
  };
}

function routeMetric(assignment, extra = {}) {
  const route = assignment.route;
  return {
    route_id: route.id,
    source_domain: route.from,
    destination_domain: route.to,
    source_chain_id: route.fromChainId,
    destination_chain_id: route.toChainId,
    source_channel: route.fromChannel,
    destination_channel: route.toChannel,
    original_worker: assignment.originalWorker,
    assigned_worker: assignment.assignedWorker,
    reassigned: assignment.reassigned ? 1 : 0,
    key_name: assignment.key.keyName,
    key_epoch: assignment.key.keyEpoch,
    queue_key: assignment.queueKey,
    ...extra,
  };
}

function hermesCommand(topology, route, worker, hermesArgs, key = keyForWorker(worker, {})) {
  const config = writeControllerHermesConfig(topology, worker, key);
  const network = dryRun ? "" : composeNetwork(topology);
  const env = [
    "-e", `RELAYER_MNEMONIC=${relayerMnemonic}`,
    "-e", "HERMES_CONFIG=/config.toml",
    "-e", `RELAYER_INDEX=${worker}`,
    "-e", `RELAYER_KEY_NAME=${key.keyName}`,
    "-e", `RELAYER_HD_PATH=m/44'/118'/${key.hdAccountIndex}'/0/0`,
    "-e", `CROSSREF_CHAIN_IDS=${topology.chains.map((chain) => chain.chainId).join(" ")}`,
  ];
  return {
    bin: "docker",
    args: [
      "run",
      "--rm",
      ...(network ? ["--network", network] : []),
      ...env,
      "-v", `${config}:/config.toml:ro`,
      "-v", `${path.join(root, "docker/scripts")}:/opt/crossref/scripts:ro`,
      "--entrypoint", "/bin/sh",
      relayerImage,
      "/opt/crossref/scripts/hermes-one-shot.sh",
      ...hermesArgs,
    ],
    route,
  };
}

function writeControllerHermesConfig(topology, worker, key) {
  const stem = path.basename(topologyFile).replace(/^topology-/, "").replace(/\.json$/, "");
  const file = path.join(root, "docker/generated", `controller-hermes-${stem}-worker-${worker}.toml`);
  const header = `[global]
log_level = "info"

[mode]

[mode.clients]
enabled = true
refresh = true
misbehaviour = false

[mode.connections]
enabled = true

[mode.channels]
enabled = true

[mode.packets]
enabled = true
clear_interval = 0
clear_on_start = false
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
  const chains = topology.chains.map((chain) => `[[chains]]
id = "${chain.chainId}"
type = "CosmosSdk"
rpc_addr = "http://${chain.service}:26657"
grpc_addr = "http://${chain.service}:9090"
event_source = { mode = "push", url = "ws://${chain.service}:26657/websocket", batch_delay = "500ms" }
rpc_timeout = "10s"
account_prefix = "crossref"
key_name = "${key.keyName}"
store_prefix = "ibc"
default_gas = 100000
max_gas = 400000
gas_price = { price = 0.0, denom = "stake" }
gas_multiplier = 1.2
max_msg_num = 1
max_tx_size = 2097152
sequential_batch_tx = true
clock_drift = "5s"
max_block_time = "10s"
trusting_period = "336hours"
trust_threshold = { numerator = "1", denominator = "3" }
memo_prefix = "crossref-controller"
`).join("\n");
  fs.writeFileSync(file, `${header}\n${chains}`);
  return file;
}

function composeNetwork(topology) {
  if (!composeFile) throw new Error("COMPOSE_FILE is required outside dry-run mode");
  const service = topology.chains[0]?.service;
  if (!service) throw new Error("topology has no chains");
  const ps = spawnSync("docker", ["compose", "-f", composeFile, "ps", "-q", service], { cwd: root, encoding: "utf8" });
  if (ps.status !== 0 || !ps.stdout.trim()) throw new Error(`could not resolve container for ${service}: ${ps.stderr || ps.stdout}`);
  const containerId = ps.stdout.trim().split(/\s+/)[0];
  const inspect = spawnSync("docker", ["inspect", "-f", "{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}", containerId], { cwd: root, encoding: "utf8" });
  if (inspect.status !== 0 || !inspect.stdout.trim()) throw new Error(`could not resolve compose network: ${inspect.stderr || inspect.stdout}`);
  return inspect.stdout.trim().split(/\s+/)[0];
}

function runCommand(command, { timeout }) {
  const result = spawnSync(command.bin, command.args, {
    cwd: root,
    encoding: "utf8",
    timeout,
    maxBuffer: 16 * 1024 * 1024,
  });
  return {
    status: result.status ?? (result.signal ? 124 : 1),
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
  };
}

function appendMetric(type, fields = {}) {
  appendJSON(metricsFile, { run_id: runId, type, time_ms: Date.now(), ...fields });
}

function appendAlert(severity, type, fields = {}) {
  appendJSON(alertsFile, { run_id: runId, severity, type, time_ms: Date.now(), ts: new Date().toISOString(), ...fields });
}

function appendJSON(file, value) {
  fs.appendFileSync(file, `${JSON.stringify(value)}\n`);
}

function readOptionalJson(file) {
  if (!file || !fs.existsSync(file)) return null;
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function countMatches(text, pattern) {
  return [...String(text).matchAll(pattern)].length;
}

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--once") parsed.once = "1";
    else if (arg === "--serve") parsed.serve = "1";
    else if (arg === "--dry-run") parsed.dryRun = "1";
    else if (arg.startsWith("--")) {
      const key = arg.slice(2).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      const next = argv[i + 1];
      if (!next || next.startsWith("--")) parsed[key] = "1";
      else {
        parsed[key] = next;
        i += 1;
      }
    }
  }
  return parsed;
}

function splitList(value) {
  return String(value || "").split(/[,\s]+/).map((item) => item.trim()).filter(Boolean);
}

function resolvePath(value) {
  if (!value) return "";
  return path.isAbsolute(value) ? value : path.join(root, value);
}

function flag(value) {
  return /^(1|true|yes)$/i.test(String(value ?? ""));
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

function nonNegativeInt(value, name) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) throw new Error(`${name} must be a non-negative integer`);
  return parsed;
}

function nonNegativeNumber(value, name) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) throw new Error(`${name} must be a non-negative number`);
  return parsed;
}

function enumValue(value, name, allowed) {
  if (!allowed.includes(value)) throw new Error(`${name} must be one of: ${allowed.join(", ")}`);
  return value;
}
