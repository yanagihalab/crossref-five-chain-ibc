import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { readJSON, writeJSON, validateInventory, topology, compose, render, directory, sshArgs, selectRelayerEndpoints, hermesConfig, extensionPlan } from "./lib.mjs";
import { composeTarget } from "./compose-bridge.mjs";
import { discoverChannel } from "./orchestrator.mjs";
import { generate } from "./generate-inventory.mjs";

const example = () => readJSON(path.join(directory, "inventory.example.json"));
const inv = () => validateInventory(example());
const temp = (fn) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "crossref-network-test-"));
  try { return fn(dir); } finally { fs.rmSync(dir, { recursive: true, force: true }); }
};

test("5 chains, 20 validators on 20 independent servers and a separate relayer", () => {
  const value = inv();
  assert.equal(value.nodes.length, 20);
  assert.equal(value.hosts.length, 21);
  assert.equal(new Set(value.nodes.map((n) => n.hostId)).size, 20);
  for (const chain of value.chains) assert.equal(chain.validators.length, 4);
  const graph = topology(value);
  assert.equal(graph.chains.length, 5);
  assert.equal(graph.pairs.length, 10);
  assert.equal(graph.routes.length, 20);
  assert.equal(new Set(graph.routes.map((r) => `${r.from}:${r.to}`)).size, 20);
});

test("inventory generator dedicates a server per validator for 3/5/6 chains and f=1/2", () => {
  for (const chains of [3, 5, 6]) for (const faults of [1, 2]) for (const layout of [undefined, "dedicated"]) {
    const value = validateInventory(generate({ chains, faults, layout }));
    assert.equal(value.nodes.length, chains * (3 * faults + 1));
    assert.equal(value.hosts.length, value.nodes.length + 1);
    assert.equal(new Set(value.nodes.map((n) => n.hostId)).size, value.nodes.length);
  }
});

test("single-BC pilot has four servers, no relayer, no IBC routes or Hermes secrets", () => temp((dir) => {
  const value = validateInventory(generate({ chains: 1, network: "pilot-202609" }));
  assert.equal(value.hosts.length, 4);
  assert.equal(value.nodes.length, 4);
  assert.equal(value.relayer, null);
  const graph = topology(value);
  assert.equal(graph.relayerCount, 0);
  assert.deepEqual(graph.routes, []);
  assert.deepEqual(graph.pairs, []);
  render(value, dir);
  assert.equal(fs.readdirSync(path.join(dir, "hosts")).length, 4);
  assert.equal(fs.existsSync(path.join(dir, "hosts/relay-1")), false);
  assert.equal(composeTarget(value, ["config", "--services"]).services.length, 4);
  assert.throws(() => composeTarget(value, ["exec", "-T", "relayer", "hermes", "version"]), /unknown remote service/);
  assert.throws(() => hermesConfig(value), /No relayer/);
}));

test("single-BC CLI operations target only four hosts; IBC operations fail before SSH", () => temp((dir) => {
  const file = path.join(dir, "inventory.json");
  writeJSON(file, generate({ chains: 1 }));
  const invoke = (command, ...extra) => spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), command,
    "--inventory", file, "--output-dir", dir, "--dry-run", ...extra], { encoding: "utf8", env: { ...process.env, CROSSREF_SSH_BIN: "/nonexistent-ssh" } });
  for (const command of ["validate", "render", "build", "prepare", "doctor", "deploy", "start", "stop", "status", "logs", "health", "restart"]) {
    const result = invoke(command);
    assert.equal(result.status, 0, `${command}: ${result.stderr}`);
    assert.doesNotMatch(result.stdout, /'relay-1'|relayer\.mnemonic/);
    if (command === "deploy") assert.equal((result.stdout.match(/\[deploy\]/g) ?? []).length, 4);
  }
  assert.equal(invoke("failover-test", "--host", "chain-a-host-1").status, 0);
  for (const command of ["channels", "experiment", "relayer-start", "relayer-stop", "relayer-refresh"]) {
    const result = invoke(command);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /requires at least [23] chains/);
    assert.equal(result.stdout, "");
  }
}));

test("multi-chain deployments require a relayer, including the two-chain IBC pilot", () => {
  const value = generate({ chains: 3 });
  value.relayer = null;
  assert.throws(() => validateInventory(value), /relayer.host/);
  const pilot = validateInventory(generate({ chains: 2 }));
  assert.equal(pilot.hosts.length, 9);
  assert.equal(pilot.nodes.length, 8);
  assert.equal(topology(pilot).pairs.length, 1);
  assert.equal(topology(pilot).routes.length, 2);
});

test("bounded bootstrap starts all twelve once; partial and non-start requests are rejected before SSH", () => temp(dir => {
  const file = path.join(dir, "inventory.json");
  writeJSON(file, generate({ chains: 3 }));
  const invoke = (command, ...extra) => spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), command,
    "--parallel-start", "--inventory", file, "--output-dir", dir, "--dry-run", ...extra], {
    encoding: "utf8", env: { ...process.env, CROSSREF_SSH_BIN: "/nonexistent-ssh" },
  });
  const result = invoke("start");
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.split('\n').filter(line => line.includes("--build")).length, 12);
  assert.equal((result.stdout.match(/http:\/\/localhost:26657\/status/g) ?? []).length, 12);
  for (const [command, ...args] of [["stop"], ["restart"], ["start", "--nodes", "chain-a-v1"], ["start", "--host", "chain-a-host-1"]]) {
    const rejected = invoke(command, ...args);
    assert.notEqual(rejected.status, 0); assert.match(rejected.stderr, /parallel-start requires/); assert.equal(rejected.stdout, "");
  }
}));

test("incremental deployment selects only new validators and the dedicated relayer", () => temp((dir) => {
  const file = path.join(dir, "inventory.json");
  writeJSON(file, generate({ chains: 2 }));
  const result = spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), "deploy", "--inventory", file,
    "--output-dir", dir, "--nodes", "chain-b-v1,chain-b-v2,chain-b-v3,chain-b-v4", "--dry-run"], {
    encoding: "utf8", env: { ...process.env, CROSSREF_SSH_BIN: "/nonexistent-ssh", CROSSREF_SCP_BIN: "/nonexistent-scp" },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal((result.stdout.match(/\[deploy\]/g) ?? []).length, 5);
  assert.doesNotMatch(result.stdout, /chain-a/);
  assert.match(result.stdout, /\[deploy\] relay-1/);
}));

test("two-chain IBC commands are enabled but the full paper experiment still needs three chains", () => temp((dir) => {
  const file = path.join(dir, "inventory.json");
  writeJSON(file, generate({ chains: 2 }));
  for (const command of ["channels", "relayer-start", "relayer-stop", "relayer-refresh", "experiment"]) {
    const result = spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), command,
      "--inventory", file, "--output-dir", dir, "--dry-run"], { encoding: "utf8", env: { ...process.env, CROSSREF_SSH_BIN: "/nonexistent-ssh" } });
    assert.equal(result.status, command === "experiment" ? 1 : 0, result.stderr);
    if (command === "experiment") assert.match(result.stderr, /requires at least 3 chains/);
  }
}));

test("extension only appends chains while preserving the running network identity", () => {
  const before = validateInventory(generate({ chains: 1 }));
  const after = validateInventory(generate({ chains: 2 }));
  const plan = extensionPlan(before, after);
  assert.deepEqual(plan.retainedChainIds, [before.chains[0].id]);
  assert.deepEqual(plan.addedNodeIds, ["chain-b-v1", "chain-b-v2", "chain-b-v3", "chain-b-v4"]);
  assert.deepEqual(plan.relayerFundingRequiredOnChains, [before.chains[0].id]);
  assert.throws(() => extensionPlan(before, before), /append/);
  for (const mutate of [
    (v) => { v.network = "changed"; },
    (v) => { v.remoteRoot = "/other"; },
    (v) => { v.denom = "other"; },
    (v) => { v.requireConsensusProof = true; },
    (v) => { v.chains[0].id = "other"; },
    (v) => { v.chains[0].validators.reverse(); },
    (v) => { v.hosts[0].address = "10.78.0.1"; },
    (v) => { v.nodes[0].ports.rpc += 10; },
  ]) {
    const changed = structuredClone(after);
    mutate(changed);
    assert.throws(() => extensionPlan(before, changed), /cannot change/);
  }
  const three = generate({ chains: 3 });
  const oldRelay = after.hosts.find((h) => h.id === after.relayer.host);
  // A stable relayer address must be retained when adding another blockchain.
  three.hosts[8].address = three.hosts[8].bindAddress = "10.78.0.20";
  three.hosts[12] = oldRelay;
  const expanded = validateInventory(three);
  assert.deepEqual(extensionPlan(after, expanded).relayerFundingRequiredOnChains, []);
  expanded.relayer.image = "hermes:changed";
  assert.throws(() => extensionPlan(after, expanded), /existing relayer/);
});

for (const [name, change, error] of [
  ["three validators", (v) => { v.chains[0].validators.pop(); }, /at least 4/],
  ["f=2 needs seven validators", (v) => { v.byzantineFaults = 2; }, /at least 7/],
  ["duplicate validator host", (v) => { v.chains[0].validators[1] = v.chains[0].validators[0]; }, /duplicate/],
  ["validator host reused across chains", (v) => { v.chains[1].validators[0] = v.chains[0].validators[0]; }, /one validator per server across all chains/],
  ["shared failure domain", (v) => { v.hosts[1].failureDomain = v.hosts[0].failureDomain; }, /failure domain/],
  ["failure domain reused across chains", (v) => { v.hosts[4].failureDomain = v.hosts[0].failureDomain; }, /failure domain/],
  ["relayer on a validator host", (v) => { v.relayer.host = v.chains[0].validators[0]; }, /separate server/],
  ["relayer shares physical domain", (v) => { v.hosts.at(-1).failureDomain = v.hosts[0].failureDomain; }, /separate server/],
  ["duplicate DNS host", (v) => { v.hosts[1].address = v.hosts[0].address; }, /duplicate SSH/],
  ["duplicate endpoint", (v) => { v.hosts[1].endpoint = v.hosts[0].bindAddress; }, /duplicate host endpoint/],
  ["duplicate bind address behind distinct endpoints", (v) => { v.hosts[1].endpoint = "validator-b.internal"; v.hosts[1].bindAddress = v.hosts[0].bindAddress; }, /duplicate host bind address/],
  ["invalid port base", (v) => { v.chains[1].portBase = 65535; }, /portBase/],
  ["unknown host", (v) => { v.chains[0].validators[0] = "missing"; }, /unknown host/],
  ["public RPC bind", (v) => { v.hosts[0].bindAddress = "0.0.0.0"; }, /private\/VPN/],
  ["public IPv4 bind", (v) => { v.hosts[0].bindAddress = "203.0.113.2"; }, /private\/VPN/],
  ["path traversal", (v) => { v.remoteRoot = "/opt/../etc"; }, /unsafe remoteRoot/],
  ["shell injection", (v) => { v.hosts[0].sshUser = "root;id"; }, /invalid sshUser/],
  ["string boolean", (v) => { v.requireConsensusProof = "false"; }, /must be boolean/],
]) test(`reject ${name}`, () => { const value = example(); change(value); assert.throws(() => validateInventory(value), error); });

test("separate servers may use the same ports", () => {
  const value = example();
  for (const chain of value.chains) chain.portBase = 26650;
  const result = validateInventory(value);
  assert.ok(result.nodes.every((n) => n.ports.rpc === 26657));
});

test("generator CLI defaults to 21 servers and refuses overwrite or colocated layout", () => temp((dir) => {
  const file = path.join(dir, "inventory.json");
  const invoke = (...args) => spawnSync(process.execPath, [path.join(directory, "generate-inventory.mjs"), "--output", file, ...args], { encoding: "utf8" });
  assert.throws(() => generate({ layout: "colocated" }), /one validator per server/);
  const rejected = invoke("--layout", "colocated");
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /one validator per server/);
  assert.equal(fs.existsSync(file), false);
  const result = invoke();
  assert.equal(result.status, 0, result.stderr);
  const contents = fs.readFileSync(file, "utf8");
  const value = validateInventory(JSON.parse(contents));
  assert.equal(value.hosts.length, 21);
  assert.equal(value.nodes.length, 20);
  assert.notEqual(invoke().status, 0);
  assert.equal(fs.readFileSync(file, "utf8"), contents);
}));

test("Compose gives each server exactly one validator, its own home and private bindings", () => {
  const value = inv();
  const bc = compose(value, value.hosts[0]);
  for (const node of value.nodes) {
    const host = value.hosts.find((h) => h.id === node.hostId);
    const result = compose(value, host);
    assert.deepEqual(Object.keys(result.services), [node.id]);
    assert.deepEqual(result.services[node.id].volumes, [`./homes/${node.id}:/var/crossref`]);
    assert.ok(result.services[node.id].ports.every((p) => p.startsWith(`${host.bindAddress}:`)));
    assert.equal(result.services[node.id].environment.CHAIN_ID, node.chainId);
  }
  assert.equal(bc.services["chain-a-v1"].ports[0], "10.77.0.11:26656:26656");
  const relay = compose(value, value.hosts.at(-1));
  assert.deepEqual(Object.keys(relay.services), ["relayer"]);
  assert.ok(relay.services.relayer.volumes.includes("./secrets/relayer.mnemonic:/run/secrets/relayer.mnemonic:ro"));
  assert.doesNotMatch(JSON.stringify(bc), /mnemonic/);
  assert.doesNotMatch(JSON.stringify(relay.services.relayer.environment), /mnemonic/i);
  assert.ok(sshArgs(value.hosts[0]).includes("StrictHostKeyChecking=yes"));
});

test("render preserves discovered channels and uses five Hermes chain entries", () => temp((dir) => {
  const value = inv();
  render(value, dir);
  const file = path.join(dir, "topology.json");
  const actual = readJSON(file);
  actual.channelsVerified = true;
  actual.routes[0].fromChannel = "channel-42";
  writeJSON(file, actual);
  render(value, dir);
  assert.equal(readJSON(file).routes[0].fromChannel, "channel-42");
  const hermes = fs.readFileSync(path.join(dir, "hosts/relay-1/hermes.toml"), "utf8");
  assert.equal((hermes.match(/\[\[chains\]\]/g) ?? []).length, 5);
  assert.match(hermes, /sequential_batch_tx = true/);
  assert.match(hermes, /^max_msg_num = 30$/m);
  assert.match(hermes, /\[rest\]\nenabled = false\nhost = "127\.0\.0\.1"\nport = 3000\n/);
  assert.match(hermes, /10\.77\.0\.11:26657/);
  assert.equal(fs.statSync(file).mode & 0o777, 0o600);
}));

test("Compose bridge routes to the right server and rejects ambiguous operations", () => {
  const value = inv();
  assert.equal(composeTarget(value, ["exec", "-T", "chain-c-v3", "crossrefd", "status"]).host.id, "chain-c-host-3");
  assert.equal(composeTarget(value, ["exec", "-T", "--index", "1", "relayer", "hermes", "version"]).host.id, "relay-1");
  assert.equal(composeTarget(value, ["run", "--rm", "--no-deps", "--entrypoint", "/bin/sh", "relayer", "/opt/crossref/scripts/hermes-one-shot.sh", "health-check"]).host.id, "relay-1");
  assert.equal(composeTarget(value, ["config", "--services"]).services.length, 21);
  assert.throws(() => composeTarget(value, ["down", "-v"]), /unsupported/);
  assert.throws(() => composeTarget(value, ["exec", "--index", "2", "relayer"]), /index must be 1/);
  assert.throws(() => composeTarget(value, ["stop", "chain-a-v1", "chain-a-v2"]), /exactly one/);
});

test("channel discovery follows connection/client state instead of sequence assumptions", async () => {
  const pair = topology(inv()).pairs[0];
  const query = async (node, args) => {
    if (args[1] === "channels") return { channels: [
      { port_id: "crossref", channel_id: "channel-8", state: "STATE_OPEN", ordering: "ORDER_UNORDERED", version: "crossref-1", connection_hops: ["connection-12"], counterparty: { port_id: "crossref", channel_id: "channel-9" } },
    ] };
    if (args[0] === "connection") return { connection: { state: "STATE_OPEN", client_id: "07-tendermint-15" } };
    if (args[0] === "client") return { client_state: { chain_id: pair.rightChainId } };
    return { channel: { state: "STATE_OPEN", ordering: "ORDER_UNORDERED", version: "crossref-1", counterparty: { channel_id: "channel-8", port_id: "crossref" } } };
  };
  assert.deepEqual(await discoverChannel(pair, {}, {}, query), { left: "channel-8", right: "channel-9" });
  assert.equal(await discoverChannel(pair, {}, {}, async (node, args) => args[0] === "client" ? { client_state: { chain_id: "wrong-chain" } } : query(node, args)), null);
  await assert.rejects(discoverChannel(pair, {}, {}, async () => ({ pagination: { next_key: "more" } })), /truncated/);
});

test("Relayer RPC selection skips a failed BC host, without changing chain identities", async () => {
  const value = inv();
  const baseline = await selectRelayerEndpoints(value, async () => true);
  const endpoints = await selectRelayerEndpoints(value, async (n) => { if (n.hostId === "chain-a-host-1") throw new Error("host down"); return true; });
  assert.equal(endpoints.length, 5);
  assert.equal(endpoints[0].host, "10.77.0.12");
  assert.equal(endpoints[0].chainId, baseline[0].chainId);
  assert.deepEqual(endpoints.slice(1), baseline.slice(1));
  const config = hermesConfig(value, endpoints);
  assert.match(config, /ws:\/\/10\.77\.0\.12:26657\/websocket/);
  assert.doesNotMatch(config, /10\.77\.0\.11/);
  await assert.rejects(selectRelayerEndpoints(value, async () => false), /no healthy RPC/);
});

test("dry-run performs no preparation or network calls", () => temp((dir) => {
  const result = spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), "deploy", "--inventory", path.join(directory, "inventory.example.json"), "--output-dir", dir, "--dry-run"], { encoding: "utf8", env: { ...process.env, CROSSREF_SSH_BIN: "/nonexistent-ssh", CROSSREF_SCP_BIN: "/nonexistent-scp" } });
  assert.equal(result.status, 0, result.stderr);
  assert.equal((result.stdout.match(/\[deploy\]/g) ?? []).length, 21);
  assert.match(result.stdout, /StrictHostKeyChecking=yes/);
  assert.equal(fs.readdirSync(dir).length, 0);
}));

test("empty node selection fails before any remote operation", () => {
  const result = spawnSync(process.execPath, [path.join(directory, "orchestrator.mjs"), "stop", "--inventory", path.join(directory, "inventory.example.json"), "--nodes", "", "--dry-run"], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /empty or duplicate/);
  assert.equal(result.stdout, "");
});

test("home installer preserves signing state, rejects a different genesis", () => temp((dir) => {
  const incoming = path.join(dir, "incoming"), root = path.join(dir, "remote");
  const source = path.join(incoming, "chain-a-v1");
  for (const file of ["config/genesis.json", "config/priv_validator_key.json", "config/node_key.json", "data/priv_validator_state.json"]) writeJSON(path.join(source, file), { value: "original" });
  const invoke = () => spawnSync("sh", [path.join(directory, "install-home.sh"), root, "chain-a-v1", incoming], { encoding: "utf8" });
  assert.equal(invoke().status, 0);
  fs.cpSync(path.join(root, "homes/chain-a-v1"), source, { recursive: true });
  writeJSON(path.join(root, "homes/chain-a-v1/data/priv_validator_state.json"), { height: "500" });
  assert.equal(invoke().status, 0);
  assert.equal(readJSON(path.join(root, "homes/chain-a-v1/data/priv_validator_state.json")).height, "500");
  writeJSON(path.join(source, "config/genesis.json"), { chain_id: "wrong-chain" });
  const failed = invoke();
  assert.notEqual(failed.status, 0);
  assert.match(failed.stderr, /identity mismatch/);
}));

test("shell entrypoints parse and Comet proof helper uses the remote RPC URL", () => temp((dir) => {
  for (const file of ["start-node.sh", "install-home.sh", "remote-hermes.sh"]) {
    const result = spawnSync("sh", ["-n", path.join(directory, file)], { encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
  }
  const script = fs.readFileSync(path.join(directory, "../../docker/scripts/run-crossref-experiment.sh"), "utf8");
  const definition = (name) => {
    const found = script.match(new RegExp(`^${name}\\(\\) \\{\\n[\\s\\S]*?^\\}`, "m"));
    assert.ok(found, name);
    return found[0];
  };
  const file = path.join(dir, "topology.json");
  writeJSON(file, topology(inv()));
  const result = spawnSync("bash", ["-c", ["set -eu", definition("topology_query"), definition("comet_consensus_proof"),
    'go() { printf "%s\\n" "$*"; }', "comet_consensus_proof chain-a 42"].join("\n")], {
    encoding: "utf8", env: { ...process.env, TOPOLOGY_FILE: file },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /http:\/\/10\.77\.0\.11:26657 42/);
  const bridge = spawnSync("bash", ["-c", ["set -eu", definition("dc"), "dc config --services"].join("\n")], {
    encoding: "utf8", env: { ...process.env, CROSSREF_COMPOSE_BRIDGE: path.join(directory, "compose-bridge.mjs"), CROSSREF_REMOTE_INVENTORY: path.join(directory, "inventory.example.json") },
  });
  assert.equal(bridge.status, 0, bridge.stderr);
  assert.equal(bridge.stdout.trim().split("\n").length, 21);
}));
