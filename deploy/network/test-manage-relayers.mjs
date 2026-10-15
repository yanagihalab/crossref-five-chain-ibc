import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { validateConfig, selectRelayers, sshArgs, remoteCommand, parseRemoteOutput, executeAction } from "./manage-relayers.mjs";

const exec = promisify(execFile);
const fixture = () => ({
  schemaVersion: 1,
  hermesOperations: {
    sshUser: "ubuntu", identityFile: "~/.ssh/test-key",
    knownHostsFiles: ["~/.ssh/known_hosts", "deploy/network/generated/known hosts"],
  },
  relayers: [1, 2, 3].map((n) => ({
    name: `crossref-relayer-${n}`, publicIPv4: `192.0.2.${n}`, hostConfigurationVerified: true,
    hermes: { workspace: `/home/ubuntu/relayer ${n}`, project: `relayer-${n}`, service: "relayer", ready: true },
  })),
});
const state = (name, extra = {}) => ({ Name: `${name}-relayer-1`, Service: "relayer", State: "running", Health: "", ExitCode: 0, ...extra });
const jsonResult = (data) => ({ stdout: JSON.stringify(data) });
const mockRun = (events, handler = () => null) => async (_config, relayer, command) => {
  const action = command.includes("--no-recreate") ? "start" : command.includes("ps -a") ? "status" : "check";
  events.push(`${relayer.name}:${action}`);
  return handler(relayer, action) ?? jsonResult(action === "check" ? { code: "ready" } : [state(relayer.name)]);
};
const testOptions = (run) => ({ run, verifySSHFiles: false });

test("target selection is explicit and rejects unknown, duplicate or injected values", () => {
  const config = validateConfig(fixture());
  assert.equal(selectRelayers(config).length, 3);
  assert.deepEqual(selectRelayers(config, "2,3").map((r) => r.name), ["crossref-relayer-2", "crossref-relayer-3"]);
  for (const target of ["0", "4", "2,2", "2,", "2;true", "02"]) assert.throws(() => selectRelayers(config, target));
});

test("configuration rejects unsafe SSH, duplicate hosts and escaping paths", () => {
  const mutate = (fn) => { const value = fixture(); fn(value); assert.throws(() => validateConfig(value)); };
  mutate((v) => { v.schemaVersion = 2; });
  mutate((v) => { v.hermesOperations.sshUser = "ubuntu;true"; });
  mutate((v) => { v.hermesOperations.knownHostsFiles = []; });
  mutate((v) => { v.hermesOperations.identityFile = "foo\nbar"; });
  mutate((v) => { v.hermesOperations.port = -1; });
  mutate((v) => { v.hermesOperations.timeoutMs = 1; });
  mutate((v) => { v.relayers[1].publicIPv4 = v.relayers[0].publicIPv4; });
  mutate((v) => { v.relayers[1].name = v.relayers[0].name; });
  for (const workspace of ["/", "relative", "/tmp/../controller", "/tmp\nfoo"]) {
    mutate((v) => { v.relayers[0].hermes.workspace = workspace; });
  }
  for (const composeFile of ["../compose.json", "/tmp/compose.json", "foo/../../compose.json", "./compose.json"]) {
    mutate((v) => { v.relayers[0].hermes.composeFile = composeFile; });
  }
  mutate((v) => { v.relayers[0].hermes.service = "relayer;true"; });
  mutate((v) => { v.relayers[0].hermes.project = "--bad"; });
  mutate((v) => { v.relayers[0].hermes.ready = "true"; });
  mutate((v) => { v.relayers[0].hostConfigurationVerified = "true"; });
});

test("SSH pins host verification and preserves paths containing spaces", async () => {
  const config = validateConfig(fixture());
  const args = sshArgs(config, config.relayers[0]);
  assert.ok(args.includes("StrictHostKeyChecking=yes"));
  assert.ok(args.includes("ForwardAgent=no"));
  assert.ok(args.includes("IdentitiesOnly=yes"));
  assert.ok(args.includes("BatchMode=yes"));
  const knownHosts = args.find((arg) => arg.startsWith("UserKnownHostsFile="));
  assert.equal(knownHosts, `UserKnownHostsFile=${config.ssh.knownHostsFiles.map((p) => `"${p}"`).join(" ")}`);
  // -G only parses configuration. It does not connect or read the private key.
  const { stdout } = await exec("ssh", ["-G", ...args]);
  const line = stdout.split("\n").find((text) => text.startsWith("userknownhostsfile "));
  assert.ok(line.includes("known hosts"));
  assert.ok(line.includes(config.ssh.knownHostsFiles[1]));
});

test("Compose ps array, single object and JSON lines are supported without secrets", () => {
  const row = state("r1", { Command: "secret-password", Environment: "private-key" });
  for (const input of [JSON.stringify([row]), JSON.stringify(row), `${JSON.stringify(row)}\n${JSON.stringify(state("r2"))}`]) {
    const result = parseRemoteOutput(input, "relayer");
    assert.equal(result.code, "containers");
    assert.equal(result.containers[0].state, "running");
    assert.ok(!JSON.stringify(result).includes("secret-password"));
    assert.ok(!JSON.stringify(result).includes("private-key"));
  }
  assert.deepEqual(parseRemoteOutput("", "relayer"), { code: "containers", containers: [] });
  for (const input of ["non-json secret", "null", JSON.stringify({ code: "secret" }), JSON.stringify(state("r", { Service: "validator" }))]) {
    assert.throws(() => parseRemoteOutput(input, "relayer"), /raw output suppressed/);
  }
});

test("list and unconfigured status never connect", async () => {
  const raw = fixture();
  for (const r of raw.relayers) { r.hermes.workspace = null; r.hermes.project = null; r.hermes.ready = false; }
  const config = validateConfig(raw);
  const events = [];
  const options = testOptions(mockRun(events));
  const list = await executeAction(config, "list", config.relayers, options);
  assert.equal(list.ok, true);
  const result = await executeAction(config, "status", config.relayers, options);
  assert.equal(result.ok, false);
  assert.ok(result.results.every((r) => r.code === "runtime-not-configured"));
  assert.deepEqual(events, []);
});

test("missing local SSH files fail without attempting a connection", async () => {
  const raw = fixture();
  raw.hermesOperations.identityFile = "/nonexistent/crossref-test-identity";
  const config = validateConfig(raw);
  const events = [];
  await assert.rejects(executeAction(config, "check", config.relayers, { run: mockRun(events) }), /no remote operation/);
  assert.deepEqual(events, []);
});

test("start rejects all targets before SSH if even one is unverified or unconfigured", async () => {
  for (const [mutate, expected] of [
    [(r) => { r.hostConfigurationVerified = false; }, "host-not-verified"],
    [(r) => { r.hermes.ready = false; }, "deployment-not-verified"],
    [(r) => { r.hermes.workspace = null; }, "runtime-not-configured"],
  ]) {
    const raw = fixture();
    mutate(raw.relayers[1]);
    const config = validateConfig(raw);
    const events = [];
    const result = await executeAction(config, "start", config.relayers, testOptions(mockRun(events)));
    assert.equal(result.ok, false);
    assert.equal(result.results[1].code, expected);
    assert.equal(result.results[0].code, "not-attempted");
    assert.deepEqual(events, []);
  }
});

test("dry run performs no SSH and no local credential reads", async () => {
  const config = validateConfig(fixture());
  const events = [];
  for (const action of ["check", "status", "start"]) {
    const result = await executeAction(config, action, config.relayers, { dryRun: true, run: mockRun(events) });
    assert.equal(result.ok, true);
    assert.equal(result.dryRun, true);
  }
  assert.deepEqual(events, []);
});

test("all selected hosts are preflighted before any start", async () => {
  const config = validateConfig(fixture());
  const events = [];
  const result = await executeAction(config, "start", config.relayers, testOptions(mockRun(events)));
  assert.equal(result.ok, true);
  assert.deepEqual(events, [1, 2, 3].map((n) => `crossref-relayer-${n}:check`).concat([1, 2, 3].map((n) => `crossref-relayer-${n}:start`)));
});

test("failed preflight blocks every mutation", async () => {
  const config = validateConfig(fixture());
  const events = [];
  const result = await executeAction(config, "start", config.relayers, testOptions(mockRun(events, (r, action) =>
    r.name.endsWith("2") && action === "check" ? jsonResult({ code: "docker-missing" }) : null)));
  assert.equal(result.ok, false);
  assert.equal(result.results[0].code, "not-attempted");
  assert.equal(result.results[1].code, "docker-missing");
  assert.equal(events.length, 3);
  assert.ok(events.every((e) => e.endsWith(":check")));
});

test("partial start failure preserves started hosts and skips remaining hosts", async () => {
  const config = validateConfig(fixture());
  const events = [];
  const result = await executeAction(config, "start", config.relayers, testOptions(mockRun(events, (r, action) =>
    r.name.endsWith("2") && action === "start" ? jsonResult({ code: "start-failed" }) : null)));
  assert.equal(result.ok, false);
  assert.equal(result.results[0].containers[0].state, "running");
  assert.equal(result.results[1].code, "start-failed");
  assert.equal(result.results[2].code, "not-attempted");
  assert.equal(events.length, 5);
});

test("remote exceptions and stderr are suppressed", async () => {
  const config = validateConfig(fixture());
  const result = await executeAction(config, "check", config.relayers, testOptions(async () => {
    throw new Error("private-key-and-password");
  }));
  assert.equal(result.ok, false);
  assert.ok(result.results.every((r) => r.code === "ssh-or-command-failed"));
  assert.ok(!JSON.stringify(result).includes("private-key-and-password"));
});

test("stopped, absent, starting or unhealthy containers are not reported as successful starts", async () => {
  const config = validateConfig(fixture());
  for (const rows of [[], [state("r", { State: "exited" })], [state("r", { Health: "starting" })], [state("r", { Health: "unhealthy" })]]) {
    const result = await executeAction(config, "start", [config.relayers[0]], testOptions(mockRun([], (_r, action) =>
      action === "start" ? jsonResult(rows) : null)));
    assert.equal(result.ok, false);
  }
});

async function localDockerFixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hermes-ops-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const workspace = path.join(root, "runtime 'quoted; $(touch NEVER)'");
  const bin = path.join(root, "bin");
  fs.mkdirSync(workspace);
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(workspace, "compose.json"), "{}\n");
  fs.writeFileSync(path.join(bin, "docker"), `#!/bin/sh
printf '%s\\n' "$*" >> "$DOCKER_TEST_LOG"
case "$*" in
  'compose version'|'info') exit 0 ;;
  *'config --services') printf 'relayer\\nvalidator\\n'; exit 0 ;;
  *'up -d --no-deps --no-recreate --no-build --pull never relayer') exit 0 ;;
  *'ps -a --format json relayer') printf '%s\\n' '{"Name":"test-relayer-1","Service":"relayer","State":"running","Health":"","ExitCode":0}'; exit 0 ;;
  *) exit 1 ;;
esac
`, { mode: 0o700 });
  const raw = fixture();
  raw.relayers[0].hermes.workspace = workspace;
  const config = validateConfig(raw);
  const log = path.join(root, "docker.log");
  const run = async (_c, _r, command) => exec("sh", ["-c", command], {
    cwd: root, env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, DOCKER_TEST_LOG: log },
  });
  return { config, workspace, root, log, run };
}

test("shell quoting survives spaces, quotes and substitutions; repeated start never recreates", async (t) => {
  const f = await localDockerFixture(t);
  const options = testOptions(f.run);
  for (let i = 0; i < 2; i++) {
    const result = await executeAction(f.config, "start", [f.config.relayers[0]], options);
    assert.equal(result.ok, true);
  }
  const log = fs.readFileSync(f.log, "utf8");
  assert.equal(log.split("\n").filter((line) => line.includes("up -d")).length, 2);
  assert.ok(log.includes("--no-deps --no-recreate --no-build --pull never relayer"));
  assert.ok(!log.includes("--force-recreate"));
  assert.ok(!/^compose .*\b(build|pull|restart|down)\b(?! never)/m.test(log.replaceAll("--no-build", "")));
  assert.equal(fs.existsSync(path.join(f.root, "NEVER")), false);
});

test("controller-owned workspace is refused before any up command", async (t) => {
  const f = await localDockerFixture(t);
  for (const marker of ["run-once.json", "results/run-once.json", "results/status.json"]) {
    const file = path.join(f.workspace, marker);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, "{}\n");
    const result = await executeAction(f.config, "start", [f.config.relayers[0]], testOptions(f.run));
    assert.equal(result.ok, false);
    assert.equal(result.results[0].code, "controller-owned-workspace");
    fs.unlinkSync(file);
  }
  assert.ok(!fs.readFileSync(f.log, "utf8").includes("up -d"));
});

test("missing workspace or compose file refuses startup", async (t) => {
  const f = await localDockerFixture(t);
  fs.unlinkSync(path.join(f.workspace, "compose.json"));
  const result = await executeAction(f.config, "start", [f.config.relayers[0]], testOptions(f.run));
  assert.equal(result.results[0].code, "compose-file-missing");
  f.config.relayers[0].hermes.workspace = path.join(f.root, "nonexistent");
  const missing = await executeAction(f.config, "start", [f.config.relayers[0]], testOptions(f.run));
  assert.equal(missing.results[0].code, "workspace-unavailable");
  assert.ok(!fs.readFileSync(f.log, "utf8").includes("up -d"));
});

test("Docker and Compose prerequisites refuse startup without mutation", async (t) => {
  const f = await localDockerFixture(t);
  const stub = path.join(f.root, "bin", "docker");
  for (const [body, expected] of [
    ["exit 1", "compose-missing"],
    ["case \"$*\" in 'compose version') exit 0 ;; *) exit 1 ;; esac", "docker-daemon-unavailable"],
    ["case \"$*\" in 'compose version'|'info') exit 0 ;; *) exit 1 ;; esac", "compose-config-invalid"],
    ["case \"$*\" in 'compose version'|'info') exit 0 ;; *'config --services') printf 'validator\\n'; exit 0 ;; *) exit 1 ;; esac", "service-missing"],
  ]) {
    fs.writeFileSync(stub, `#!/bin/sh\n${body}\n`, { mode: 0o700 });
    const result = await executeAction(f.config, "start", [f.config.relayers[0]], testOptions(f.run));
    assert.equal(result.ok, false);
    assert.equal(result.results[0].code, expected);
  }
});

test("remote command has no install, experiment, power or recovery action", () => {
  const config = validateConfig(fixture());
  const command = remoteCommand(config.relayers[0], "start");
  for (const word of ["apt-get", "systemctl", "shutdown", "reboot", "recover-failed", "advance", "--force-recreate"]) {
    assert.ok(!command.includes(word));
  }
});

test("wrapper works from outside the repo and defaults to help", async () => {
  const wrapper = path.join(import.meta.dirname, "manage-relayers.sh");
  const { stdout } = await exec("bash", [wrapper], { cwd: os.tmpdir(), env: { ...process.env, NODE_BIN: process.execPath } });
  assert.ok(stdout.includes("Usage:"));
  const list = await exec("bash", [wrapper, "list", "2,3", "--json"], { cwd: os.tmpdir(), env: { ...process.env, NODE_BIN: process.execPath } });
  assert.equal(JSON.parse(list.stdout).results.length, 2);
  await assert.rejects(exec("bash", [wrapper, "start", "all"], { env: { ...process.env, NODE_BIN: process.execPath } }), (error) =>
    error.code === 1 && error.stdout.includes("host-not-verified"));
});
