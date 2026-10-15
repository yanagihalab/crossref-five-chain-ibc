import fs from "node:fs";
import path from "node:path";
import { isIP } from "node:net";
import { execFile } from "node:child_process";
import { promisify, parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { quote, repoRoot, resolvePath } from "./lib.mjs";

const execFileAsync = promisify(execFile);
const defaultConfig = "deploy/network/multi-relayer.example.json";
const actions = new Set(["list", "check", "status", "start"]);
const safeLabel = (value) => typeof value === "string" && /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(value);
const safePath = (value) => typeof value === "string" && value.length > 0 && !/[\x00-\x1f\x7f]/.test(value);

export const help = `Usage: bash deploy/network/manage-relayers.sh ACTION [TARGET] [OPTIONS]

Actions:
  list     Show configured Relayers (no SSH).
  check    Check SSH, Docker, Compose and deployment prerequisites (read-only).
  status   Show Hermes container state (read-only).
  start    Start only the selected, verified Hermes service(s).

Targets: 1, 2, 3, 2,3, all (default: all).
Options: --config FILE  --dry-run  --json  --help

No VPS power operations, experiment startup, or automatic recovery.
Start requires hostConfigurationVerified=true and hermes.ready=true.
`;

export function validateConfig(raw) {
  if (raw?.schemaVersion !== 1 || !Array.isArray(raw.relayers) || raw.relayers.length === 0) {
    throw new Error("Configuration requires schemaVersion=1 and relayers.");
  }
  const ssh = raw.hermesOperations;
  if (!ssh || !/^[a-z_][a-z0-9_-]*$/.test(ssh.sshUser ?? "")) throw new Error("Invalid hermesOperations.sshUser.");
  if (!safePath(ssh.identityFile) || !Array.isArray(ssh.knownHostsFiles) ||
      ssh.knownHostsFiles.length === 0 || !ssh.knownHostsFiles.every(safePath)) {
    throw new Error("Explicit SSH identityFile and knownHostsFiles are required.");
  }
  const timeoutMs = ssh.timeoutMs ?? 90_000;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1_000 || timeoutMs > 300_000) throw new Error("Invalid SSH timeoutMs.");
  const port = ssh.port ?? 22;
  if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) throw new Error("Invalid SSH port.");
  const names = new Set();
  const addresses = new Set();
  const relayers = raw.relayers.map((item) => {
    if (!/^crossref-relayer-[1-9][0-9]*$/.test(item.name ?? "") || names.has(item.name)) {
      throw new Error("Invalid or duplicate Relayer name.");
    }
    if (isIP(item.publicIPv4) !== 4 || addresses.has(item.publicIPv4)) throw new Error("Invalid or duplicate Relayer IPv4 address.");
    names.add(item.name);
    addresses.add(item.publicIPv4);
    const hermes = item.hermes ?? {};
    const workspace = hermes.workspace ?? null;
    if (workspace !== null && (!safePath(workspace) || !path.posix.isAbsolute(workspace) ||
        path.posix.normalize(workspace) !== workspace || workspace === "/")) {
      throw new Error(`${item.name}: workspace must be a normalized absolute path other than /.`);
    }
    const project = hermes.project ?? null;
    if (project !== null && (typeof project !== "string" || !/^[a-z0-9][a-z0-9_-]{0,62}$/.test(project))) {
      throw new Error(`${item.name}: invalid Compose project.`);
    }
    const composeFile = hermes.composeFile ?? "compose.json";
    if (!safePath(composeFile) || path.posix.isAbsolute(composeFile) ||
        composeFile.split("/").some((part) => part === ".." || part === "." || part === "")) {
      throw new Error(`${item.name}: composeFile must stay inside workspace.`);
    }
    const service = hermes.service ?? "relayer";
    if (!safeLabel(service)) throw new Error(`${item.name}: invalid Hermes service.`);
    if (hermes.ready !== undefined && typeof hermes.ready !== "boolean") throw new Error(`${item.name}: ready must be boolean.`);
    if (item.hostConfigurationVerified !== undefined && typeof item.hostConfigurationVerified !== "boolean") {
      throw new Error(`${item.name}: hostConfigurationVerified must be boolean.`);
    }
    return {
      name: item.name, address: item.publicIPv4, provisioning: item.provisioning,
      hostConfigurationVerified: item.hostConfigurationVerified === true,
      hermes: { workspace, project, composeFile, service, ready: hermes.ready === true },
      hostSetup: item.hostSetup,
    };
  });
  return {
    ssh: { user: ssh.sshUser, port, timeoutMs, identityFile: resolvePath(ssh.identityFile),
      knownHostsFiles: ssh.knownHostsFiles.map(resolvePath) },
    relayers,
  };
}

export function selectRelayers(config, target = "all") {
  if (target === "all") return config.relayers;
  const numbers = target.split(",");
  if (numbers.length === 0 || numbers.some((n) => !/^[1-9][0-9]*$/.test(n)) || new Set(numbers).size !== numbers.length) {
    throw new Error("Target must be all or distinct Relayer numbers, e.g. 2,3.");
  }
  return numbers.map((n) => {
    const relayer = config.relayers.find((item) => item.name === `crossref-relayer-${n}`);
    if (!relayer) throw new Error(`Relayer ${n} is not configured.`);
    return relayer;
  });
}

function configured(relayer) {
  return Boolean(relayer.hermes.workspace && relayer.hermes.project);
}

function startBlocker(relayer) {
  if (!configured(relayer)) return "runtime-not-configured";
  if (!relayer.hostConfigurationVerified) return "host-not-verified";
  if (!relayer.hermes.ready) return "deployment-not-verified";
  return null;
}

export function sshArgs(config, relayer) {
  // OpenSSH parses this option again; quoting each path preserves spaces in it.
  const knownHosts = config.ssh.knownHostsFiles.map((file) => `"${file.replace(/["\\]/g, "\\$&")}"`).join(" ");
  return ["-F", "/dev/null", "-T", "-p", String(config.ssh.port), "-i", config.ssh.identityFile,
    "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "PasswordAuthentication=no",
    "-o", "KbdInteractiveAuthentication=no", "-o", "ForwardAgent=no", "-o", "UpdateHostKeys=no",
    "-o", "StrictHostKeyChecking=yes", "-o", `UserKnownHostsFile=${knownHosts}`,
    "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1",
    "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2",
    `${config.ssh.user}@${relayer.address}`];
}

function compose(relayer) {
  const h = relayer.hermes;
  return `docker compose -p ${quote(h.project)} -f ${quote(h.composeFile)}`;
}

function runtimeGuard(relayer) {
  return `cd -P -- ${quote(relayer.hermes.workspace)} 2>/dev/null || reply workspace-unavailable
for marker in run-once.json results/run-once.json results/status.json; do
  if [ -e "$marker" ]; then reply controller-owned-workspace; fi
done
if [ ! -f ${quote(relayer.hermes.composeFile)} ]; then reply compose-file-missing; fi
services=$(${compose(relayer)} config --services 2>/dev/null) || reply compose-config-invalid
printf '%s\\n' "$services" | grep -Fx -- ${quote(relayer.hermes.service)} >/dev/null || reply service-missing`;
}

export function remoteCommand(relayer, action) {
  const header = `set -eu
reply() { printf '{"code":"%s"}\\n' "$1"; exit 0; }
command -v docker >/dev/null 2>&1 || reply docker-missing
docker compose version >/dev/null 2>&1 || reply compose-missing
docker info >/dev/null 2>&1 || reply docker-daemon-unavailable`;
  let script = header;
  if (!configured(relayer)) script += "\nreply runtime-not-configured";
  else {
    script += `\n${runtimeGuard(relayer)}`;
    if (action === "check") script += "\nreply ready";
    else if (action === "status") {
      script += `\n${compose(relayer)} ps -a --format json ${quote(relayer.hermes.service)} 2>/dev/null`;
    } else if (action === "start") {
      script += `\n${compose(relayer)} up -d --no-deps --no-recreate --no-build --pull never ${quote(relayer.hermes.service)} >/dev/null 2>&1 || reply start-failed
${compose(relayer)} ps -a --format json ${quote(relayer.hermes.service)} 2>/dev/null`;
    } else throw new Error("Invalid remote action.");
  }
  return `sh -c ${quote(script)}`;
}

const remoteCodes = new Set(["ready", "docker-missing", "compose-missing", "docker-daemon-unavailable",
  "runtime-not-configured", "workspace-unavailable", "controller-owned-workspace", "compose-file-missing",
  "compose-config-invalid", "service-missing", "start-failed"]);

export function parseRemoteOutput(stdout, service) {
  const value = stdout.trim();
  let rows;
  try {
    let parsed;
    try { parsed = value ? JSON.parse(value) : []; }
    catch { parsed = value.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line)); }
    if (!Array.isArray(parsed) && Object.hasOwn(parsed, "code")) {
      if (!remoteCodes.has(parsed.code)) throw new Error("unknown code");
      return { code: parsed.code };
    }
    rows = Array.isArray(parsed) ? parsed : [parsed];
    if (rows.some((row) => !row || row.Service !== service || !safeLabel(row.Name) || !safeLabel(row.State) ||
        (row.Health !== undefined && row.Health !== "" && !safeLabel(row.Health)))) throw new Error("invalid state");
  } catch {
    throw new Error("Invalid or unexpected remote response (raw output suppressed).");
  }
  return {
    code: "containers",
    containers: rows.map((row) => ({ name: row.Name, state: row.State, health: row.Health || "none",
      exitCode: Number.isSafeInteger(row.ExitCode) ? row.ExitCode : null })),
  };
}

export async function runSSH(config, relayer, command, { timeoutMs = config.ssh.timeoutMs, maxBuffer = 256 * 1024 } = {}) {
  // execFile avoids local shell expansion. Remote stderr may contain secrets; never relay it.
  return execFileAsync("ssh", [...sshArgs(config, relayer), command], {
    timeout: timeoutMs, maxBuffer,
  });
}

function resultIsRunning(result) {
  return result.code === "containers" && result.containers.length > 0 &&
    result.containers.every((row) => row.state === "running" && ["none", "healthy"].includes(row.health));
}

export function ensureSSHFiles(config) {
  for (const file of [config.ssh.identityFile, ...config.ssh.knownHostsFiles]) {
    try { fs.accessSync(file, fs.constants.R_OK); if (!fs.statSync(file).isFile()) throw new Error(); }
    catch { throw new Error("SSH identity or known-hosts file is unavailable; no remote operation performed."); }
  }
}

export async function executeAction(config, action, relayers, { dryRun = false, run = runSSH, verifySSHFiles = true } = {}) {
  if (!actions.has(action)) throw new Error("Invalid action.");
  if (action === "list") return { ok: true, results: relayers.map((r) => ({ name: r.name, address: r.address,
    code: startBlocker(r) ?? "configured", workspace: r.hermes.workspace, service: r.hermes.service })) };
  if (action === "start" && relayers.some(startBlocker)) {
    return { ok: false, results: relayers.map((r) => ({ name: r.name,
      code: startBlocker(r) ?? "not-attempted", action })) };
  }
  if (dryRun) return { ok: true, dryRun: true, results: relayers.map((r) => ({ name: r.name, action,
    code: "dry-run", steps: action === "start" ? ["read-only-preflight", "hermes-only-start", "container-status"] : [action] })) };
  const requiresSSH = action !== "status" || relayers.some(configured);
  if (requiresSSH && verifySSHFiles) ensureSSHFiles(config);
  const inspect = async (r, operation) => {
    try {
      const { stdout } = await run(config, r, remoteCommand(r, operation));
      const result = { name: r.name, action: operation, ...parseRemoteOutput(stdout, r.hermes.service) };
      if (operation === "check" && result.code === "ready") {
        result.startReady = startBlocker(r) === null;
        result.startBlocker = startBlocker(r);
      }
      return result;
    } catch {
      return { name: r.name, action: operation, code: "ssh-or-command-failed" };
    }
  };
  if (action !== "start") {
    const results = [];
    for (const r of relayers) {
      results.push(action === "status" && !configured(r) ? { name: r.name, action, code: "runtime-not-configured" } : await inspect(r, action));
    }
    return { ok: results.every((r) => r.code === "ready" || resultIsRunning(r)), results };
  }
  // Validate every selected host before starting any of them. Partial starts are not rolled back.
  const preflight = [];
  for (const r of relayers) preflight.push(await inspect(r, "check"));
  if (preflight.some((r) => r.code !== "ready")) return { ok: false, results: preflight.map((r) => ({ ...r,
    action, code: r.code === "ready" ? "not-attempted" : r.code })) };
  const results = [];
  let failed = false;
  for (const r of relayers) {
    if (failed) { results.push({ name: r.name, action, code: "not-attempted" }); continue; }
    const result = await inspect(r, action);
    failed = !resultIsRunning(result);
    results.push(result);
  }
  return { ok: !failed, results };
}

export async function main(argv = process.argv.slice(2)) {
  const { values, positionals } = parseArgs({ args: argv, allowPositionals: true, options: {
    config: { type: "string", default: defaultConfig }, "dry-run": { type: "boolean", default: false },
    json: { type: "boolean", default: false }, help: { type: "boolean", short: "h" },
  } });
  if (values.help || positionals.length === 0 || positionals[0] === "help") { process.stdout.write(help); return 0; }
  const [action, target = "all"] = positionals;
  if (!actions.has(action) || positionals.length > 2) throw new Error("Expected ACTION and optional TARGET; use --help.");
  let raw;
  try { raw = JSON.parse(fs.readFileSync(path.resolve(repoRoot, values.config), "utf8")); }
  catch { throw new Error("Cannot read configuration JSON."); }
  const config = validateConfig(raw);
  const selected = selectRelayers(config, target);
  const output = await executeAction(config, action, selected, { dryRun: values["dry-run"] });
  if (values.json) process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
  else {
    for (const result of output.results) {
      process.stdout.write(`${result.name}: ${result.code}${result.containers ? ` (${result.containers.map((c) => `${c.name}: ${c.state}, health=${c.health}`).join("; ") || "no containers"})` : ""}\n`);
      if (result.startReady === false) process.stdout.write(`  Start blocked: ${result.startBlocker}\n`);
    }
    if (output.dryRun) process.stdout.write("Dry run: no SSH or remote changes.\n");
    if (action === "start" && !output.ok) process.stdout.write("Startup incomplete or blocked; no automatic recovery.\n");
  }
  return output.ok ? 0 : 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
