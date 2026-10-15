#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { readBlockTimeProfile } from "./block-time.mjs";
import { directory, repoRoot, readJSON, write, writeJSON, quote, hash, fingerprint, resolvePath,
  loadInventory, render, topology, hostFor, hostRoot, project, sshArgs, adminProxyOptions, remoteCompose, hermesConfig, selectRelayerEndpoints, extensionPlan } from "./lib.mjs";

export async function run(program, args, { capture = false, quiet = false, env = process.env, dryRun = false } = {}) {
  if (dryRun) {
    console.log([program, ...args].map(quote).join(" "));
    return "";
  }
  return new Promise((resolve, reject) => {
    const child = spawn(program, args, { cwd: repoRoot, env, stdio: ["ignore", capture || quiet ? "pipe" : "inherit", capture || quiet ? "pipe" : "inherit"] });
    let stdout = "", stderr = "";
    child.stdout?.on("data", (part) => { stdout += part; });
    child.stderr?.on("data", (part) => { stderr += part; });
    child.once("error", reject);
    child.once("close", (code) => code === 0 ? resolve(stdout) : reject(new Error(`${path.basename(program)} failed (${code})${quiet ? "; secret-bearing output suppressed" : `: ${stderr.slice(-4000)}`}`)));
  });
}

export async function prepare(inv, out, binary, base = null, { configureGenesis, configOverrides, appOverrides, relayerCount = 1 } = {}) {
  if (![1, 3].includes(relayerCount) || (base && relayerCount !== 1)) throw new Error("Multiple relayer identities require a fresh generation");
  const timeoutCommit = readBlockTimeProfile(inv, out)?.timeoutCommit ?? "2s";
  let plan, prior;
  if (base) {
    // Canonicalize the existing parent to reject aliases/symlinks into the live templates.
    fs.mkdirSync(out, { recursive: true, mode: 0o700 });
    const oldPath = fs.realpathSync(base.out), newPath = fs.realpathSync(out);
    if (oldPath === newPath || newPath.startsWith(`${oldPath}${path.sep}`) || oldPath.startsWith(`${newPath}${path.sep}`)) {
      throw new Error("Extension requires a separate output directory, not inside or around the existing deployment");
    }
    plan = extensionPlan(base.inv, inv);
    prior = verifyPrepared(base.inv, base.out);
  }
  const prepared = path.join(out, "prepared");
  if (fs.existsSync(prepared)) {
    const existing = verifyPrepared(inv, out);
    if (relayerCount === 3 && existing.relayers?.length !== 3) throw new Error("Prepared relayer count differs");
    if (base && existing.extension?.baseInventoryHash !== fingerprint(base.inv)) throw new Error("Extension base does not match the prepared ancestry");
    console.log("Prepared identities already exist; preserving keys and genesis.");
    return;
  }
  const staging = path.join(out, `.prepare-${randomUUID()}`);
  fs.mkdirSync(staging, { recursive: true, mode: 0o700 });
  const cli = (home, args, secret = false) => run(binary, ["--home", home, ...args], { capture: true, quiet: secret });
  const manifest = prior ? { ...structuredClone(prior), inventoryHash: fingerprint(inv),
    extension: { ...plan, baseInventoryHash: fingerprint(base.inv), baseOutputDir: fs.realpathSync(base.out) } }
    : { inventoryHash: fingerprint(inv), genesisHashes: {}, nodes: {}, relayerAddress: "" };
  try {
    if (base) {
      for (const node of base.inv.nodes) {
        fs.cpSync(path.join(base.out, "prepared/homes", node.id), path.join(staging, "homes", node.id), { recursive: true, errorOnExist: true, force: false });
      }
      if (base.inv.relayer) write(path.join(staging, "relayer.mnemonic"), fs.readFileSync(path.join(base.out, "prepared/relayer.mnemonic")));
    }
    const relayerHome = path.join(staging, "relayer-home");
    let relayerKey = base?.inv.relayer ? { address: manifest.relayerAddress } : null;
    if (inv.relayer && !relayerKey) {
      relayerKey = JSON.parse(await cli(relayerHome, ["keys", "add", "relayer", "--keyring-backend", "test", "--output", "json"], true));
      if (!relayerKey.mnemonic || !relayerKey.address) throw new Error("SDK did not produce relayer identity");
      write(path.join(staging, "relayer.mnemonic"), `${relayerKey.mnemonic}\n`);
      manifest.relayerAddress = relayerKey.address;
    }
    const relayAddresses = relayerKey ? [relayerKey.address] : [];
    if (relayerCount === 3) {
      manifest.relayers = [{ name: "relayer-1", address: relayerKey.address, mnemonicFile: "relayer.mnemonic" }];
      for (const index of [2, 3]) {
        const name = `relayer-${index}`;
        const key = JSON.parse(await cli(relayerHome, ["keys", "add", name, "--keyring-backend", "test", "--output", "json"], true));
        if (!key.mnemonic || !key.address || relayAddresses.includes(key.address)) throw new Error("Invalid independent relayer identity");
        write(path.join(staging, `${name}.mnemonic`), `${key.mnemonic}\n`);
        relayAddresses.push(key.address);
        manifest.relayers.push({ name, address: key.address, mnemonicFile: `${name}.mnemonic` });
      }
    }
    for (const chain of inv.chains) {
      if (plan?.retainedChainIds.includes(chain.id)) continue;
      console.log(`[prepare] ${chain.id}: ${chain.validators.length} validators`);
      const nodes = inv.nodes.filter((n) => n.chainId === chain.id);
      const homes = nodes.map((n) => path.join(staging, "homes", n.id));
      for (let i = 0; i < nodes.length; i++) {
        const node = nodes[i], home = homes[i];
        await cli(home, ["init", node.id, "--chain-id", chain.id, "--default-denom", inv.denom], true);
        const key = JSON.parse(await cli(home, ["keys", "add", "validator", "--keyring-backend", "test", "--output", "json"], true));
        const consensus = readJSON(path.join(home, "config/priv_validator_key.json"));
        const nodeId = (await cli(home, ["comet", "show-node-id"])).trim();
        manifest.nodes[node.id] = { address: key.address, consensusAddress: consensus.address, nodeId,
          keyHashes: Object.fromEntries(["priv_validator_key.json", "node_key.json"].map((file) => [file, hash(fs.readFileSync(path.join(home, "config", file)))])) };
      }
      const genesisFile = (home) => path.join(home, "config/genesis.json");
      const genesis = readJSON(genesisFile(homes[0]));
      const params = genesis.app_state.crossref.params;
      params.require_strict_hysteresis_signature = true;
      params.require_consensus_proof = inv.requireConsensusProof;
      params.checkpoint_proof_max_lag = String(inv.checkpointProofMaxLag);
      if (configureGenesis) configureGenesis(genesis, chain);
      writeJSON(genesisFile(homes[0]), genesis);
      for (const address of [...nodes.map((n) => manifest.nodes[n.id].address), ...relayAddresses]) {
        await cli(homes[0], ["genesis", "add-genesis-account", address, `100000000000${inv.denom}`, "--keyring-backend", "test"]);
      }
      const common = fs.readFileSync(genesisFile(homes[0]));
      for (let i = 0; i < nodes.length; i++) {
        const node = nodes[i], home = homes[i], host = hostFor(inv, node.hostId);
        write(genesisFile(home), common);
        await cli(home, ["genesis", "gentx", "validator", `100000000${inv.denom}`, "--chain-id", chain.id,
          "--keyring-backend", "test", "--ip", host.endpoint, "--p2p-port", String(node.ports.p2p)]);
        if (i > 0) for (const file of fs.readdirSync(path.join(home, "config/gentx"))) {
          write(path.join(homes[0], "config/gentx", file), fs.readFileSync(path.join(home, "config/gentx", file)));
        }
      }
      await cli(homes[0], ["genesis", "collect-gentxs"]);
      await cli(homes[0], ["genesis", "validate"]);
      const finalGenesis = fs.readFileSync(genesisFile(homes[0]));
      manifest.genesisHashes[chain.id] = hash(finalGenesis);
      for (let i = 0; i < nodes.length; i++) {
        const node = nodes[i], home = homes[i];
        write(genesisFile(home), finalGenesis);
        const peers = nodes.filter((n) => n.id !== node.id).map((n) => `${manifest.nodes[n.id].nodeId}@${hostFor(inv, n.hostId).endpoint}:${n.ports.p2p}`).join(",");
        for (const [key, value] of Object.entries({
          "p2p.persistent_peers": peers, "p2p.external_address": `${hostFor(inv, node.hostId).endpoint}:${node.ports.p2p}`,
          "p2p.addr_book_strict": "false", "p2p.allow_duplicate_ip": "true", "p2p.pex": "false",
          "consensus.timeout_commit": timeoutCommit,
          ...(configOverrides ? configOverrides(chain) : {}),
        })) await cli(home, ["config", "set", "config", key, value, "--skip-validate"]);
        // Confix lacks a CometBFT 0.39 validator; real node startup validates these files.
        // Keep proofs inside the retained application-state window.
        for (const [key, value] of Object.entries({ pruning: "custom", "pruning-keep-recent": String(inv.checkpointProofMaxLag + 100), "pruning-interval": "10",
          ...(appOverrides ? appOverrides(chain) : {}) })) {
          await cli(home, ["config", "set", "app", key, value, "--skip-validate"]);
        }
      }
    }
    for (const field of ["address", "consensusAddress", "nodeId"]) {
      const values = Object.values(manifest.nodes).map((n) => n[field]);
      if (values.some((value) => !value) || new Set(values).size !== values.length) throw new Error(`duplicate or missing generated ${field}`);
    }
    if (fs.existsSync(relayerHome)) fs.rmSync(relayerHome, { recursive: true });
    writeJSON(path.join(staging, "manifest.json"), manifest);
    protectTree(staging);
    // Validate the whole candidate before making it the published prepared directory.
    verifyPrepared(inv, out, staging);
    if (base) verifyPrepared(base.inv, base.out);
    fs.renameSync(staging, prepared);
    verifyPrepared(inv, out);
    console.log(`Prepared ${inv.nodes.length} independent validator identities. Keep ${prepared} private.`);
  } catch (error) {
    // Only this invocation's incomplete, never-deployed staging directory is removed.
    fs.rmSync(staging, { recursive: true, force: true });
    throw error;
  }
}

function protectTree(dir) {
  fs.chmodSync(dir, 0o700);
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) protectTree(file);
    else if (entry.isFile()) fs.chmodSync(file, 0o600);
    else throw new Error(`unexpected non-regular prepared file: ${file}`);
  }
}

export function verifyPrepared(inv, out, dir = path.join(out, "prepared")) {
  const manifest = readJSON(path.join(dir, "manifest.json"));
  if (manifest.inventoryHash !== fingerprint(inv)) throw new Error("Prepared inventory mismatch; never regenerate a live network's identities");
  for (const n of inv.nodes) {
    const home = path.join(dir, "homes", n.id);
    if (hash(fs.readFileSync(path.join(home, "config/genesis.json"))) !== manifest.genesisHashes[n.chainId]) throw new Error(`${n.id}: genesis hash mismatch`);
    if (readJSON(path.join(home, "config/priv_validator_key.json")).address !== manifest.nodes[n.id]?.consensusAddress) throw new Error(`${n.id}: consensus identity mismatch`);
    for (const [file, expected] of Object.entries(manifest.nodes[n.id].keyHashes)) {
      if (hash(fs.readFileSync(path.join(home, "config", file))) !== expected) throw new Error(`${n.id}: prepared key mismatch`);
    }
    const state = readJSON(path.join(home, "data/priv_validator_state.json"));
    if (String(state.height) !== "0") throw new Error(`${n.id}: prepared home has already signed; do not deploy signing-state clones`);
  }
  return manifest;
}

export async function main(argv = process.argv.slice(2)) {
  const command = argv[0] ?? "help";
  const { values: opts } = parseArgs({ args: argv.slice(1), options: {
    inventory: { type: "string", default: "deploy/network/inventory.json" },
    "output-dir": { type: "string", default: "deploy/network/generated" },
    "base-inventory": { type: "string" }, "base-output-dir": { type: "string" },
    "reuse-binary-from": { type: "string" },
    binary: { type: "string", default: "build/crossrefdd" },
    "build-dir": { type: "string", default: "build" },
    "dry-run": { type: "boolean", default: false }, nodes: { type: "string" }, host: { type: "string" },
    "parallel-start": { type: "boolean", default: false },
    "checkpoint-height": { type: "string", default: "1" },
    tail: { type: "string", default: "50" },
  } });
  const commands = ["validate", "render", "build", "prepare", "extend", "doctor", "deploy", "start", "stop", "restart", "status", "logs", "health", "channels", "relayer-start", "relayer-stop", "relayer-refresh", "failover-test", "experiment"];
  if (command === "help") {
    console.log(`Crossref distributed lab (VPS / on-premises)\nCommands: ${commands.join(", ")}\nOptions: --inventory PATH --output-dir PATH --binary PATH --dry-run --nodes ID,... --host ID --checkpoint-height N\nExtension: --base-inventory PATH --base-output-dir PATH (use a separate output directory)\nSee deploy/network/README.ja.md. No provisioning, reset, or key rotation is implicit.`);
    return;
  }
  if (!commands.includes(command)) throw new Error(`unknown command: ${command}`);
  if (opts["parallel-start"] && (command !== "start" || opts.nodes !== undefined || opts.host !== undefined))
    throw new Error("parallel-start requires a full-generation start without --nodes or --host");
  const inventoryFile = resolvePath(opts.inventory), out = resolvePath(opts["output-dir"]);
  const inv = loadInventory(inventoryFile), dryRun = opts["dry-run"];
  const reuseBinaryFrom = opts["reuse-binary-from"];
  if (reuseBinaryFrom && (command !== "deploy" || path.posix.dirname(reuseBinaryFrom) !== inv.remoteRoot || !/^[a-z0-9-]+$/.test(path.posix.basename(reuseBinaryFrom)))) throw new Error("reuse-binary-from must be a sibling generation directory under remoteRoot (deploy only)");
  const minimumChains = command === "experiment" ? 3 : 2;
  if (["channels", "experiment", "relayer-start", "relayer-stop", "relayer-refresh"].includes(command) && (!inv.relayer || inv.chains.length < minimumChains)) {
    throw new Error(`${command} requires at least ${minimumChains} chains and a relayer; use health or failover-test for the single-BC pilot`);
  }
  let nodes = inv.nodes;
  if (opts.nodes !== undefined) {
    const ids = opts.nodes.split(",");
    if (ids.some((id) => !id) || new Set(ids).size !== ids.length) throw new Error("empty or duplicate --nodes selection");
    if (ids.some((id) => !inv.nodes.some((n) => n.id === id))) throw new Error("unknown --nodes selection");
    nodes = inv.nodes.filter((n) => ids.includes(n.id));
  }
  if (opts.host !== undefined && !inv.hosts.some((h) => h.id === opts.host)) throw new Error("unknown --host");
  if (opts.host) nodes = nodes.filter((n) => n.hostId === opts.host);
  const selectedHosts = inv.hosts.filter((h) => nodes.some((n) => n.hostId === h.id));
  const ssh = (host, body, capture = false) => run(process.env.CROSSREF_SSH_BIN ?? "ssh", [...sshArgs(host), body], { capture, dryRun });
  const dc = (host, args, capture = false) => ssh(host, remoteCompose(inv, host, args), capture);
  const relayerHost = hostFor(inv, inv.relayer?.host);
  const operationHosts = [...selectedHosts, ...(relayerHost ? [relayerHost] : [])];
  const hermes = (args, capture = true) => dc(relayerHost, ["run", "--rm", "--no-deps", "relayer", ...args], capture);
  const rpc = async (node, endpoint) => JSON.parse(await dc(hostFor(inv, node.hostId), ["exec", "-T", node.id, "curl", "--max-time", "15", "-fsS", `http://localhost:26657/${endpoint}`], true));
  const queryChain = async (node, args) => JSON.parse(await dc(hostFor(inv, node.hostId),
    ["exec", "-T", node.id, "crossrefd", "--home", "/var/crossref", "query", "ibc", ...args, "--output", "json"], true));
  const snapshot = async (selected) => {
    const result = {};
    const manifest = dryRun ? null : verifyPrepared(inv, out);
    for (const node of selected) {
      if (dryRun) { await rpc(node, "status").catch(() => {}); continue; }
      const status = (await rpc(node, "status")).result;
      if (status?.node_info?.network !== node.chainId || status?.sync_info?.catching_up !== false || BigInt(status.sync_info.latest_block_height) < 1n) throw new Error(`${node.id}: wrong chain or not synchronized`);
      const validators = (await rpc(node, "validators?per_page=100")).result;
      const expected = inv.nodes.filter((n) => n.chainId === node.chainId).map((n) => manifest.nodes[n.id].consensusAddress.toUpperCase()).sort();
      const actual = validators?.validators ?? [];
      if (Number(validators?.total) !== expected.length || JSON.stringify(actual.map((v) => v.address.toUpperCase()).sort()) !== JSON.stringify(expected)) throw new Error(`${node.id}: unexpected validator set`);
      if (new Set(actual.map((v) => v.voting_power)).size !== 1 || BigInt(actual[0].voting_power) < 1n) throw new Error(`${node.id}: unequal/zero validator voting power`);
      result[node.id] = status.sync_info.latest_block_height;
    }
    return result;
  };
  const advancing = async (selected) => {
    const before = await snapshot(selected);
    if (dryRun) return;
    for (let attempt = 0; attempt < 20; attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 3000));
      const after = await snapshot(selected);
      if (selected.every((n) => BigInt(after[n.id]) > BigInt(before[n.id]))) return after;
    }
    throw new Error("block height did not advance within 60 seconds");
  };
  const waitHealthy = async (selected) => {
    const deadline = Date.now() + 180000;
    for (;;) {
      try { return await advancing(selected); } catch (error) {
        if (Date.now() >= deadline || dryRun) throw error;
        await new Promise((resolve) => setTimeout(resolve, 3000));
      }
    }
  };
  if (["validate", "render"].includes(command)) {
    if (command === "render" && !dryRun) render(inv, out);
    console.log(`${inv.chains.length} chains / ${inv.nodes.length} validators / ${inv.hosts.length - (inv.relayer ? 1 : 0)} BC hosts / ${inv.relayer ? "separate relayer" : "no relayer (consensus-only pilot)"}; f=${inv.byzantineFaults}`);
  } else if (command === "build") {
    const archs = [...new Set(inv.hosts.filter((h) => h.id !== inv.relayer?.host).map((h) => h.architecture))];
    if (!dryRun) fs.mkdirSync(resolvePath(opts["build-dir"]), { recursive: true });
    await run("go", ["build", "-p", "2", "-o", resolvePath(opts.binary), "./cmd/crossrefdd"], {
      dryRun, env: { ...process.env, GOOS: os.platform(), GOARCH: os.arch() === "x64" ? "amd64" : os.arch() },
    });
    for (const arch of archs) await run("go", ["build", "-p", "2", "-o", path.join(resolvePath(opts["build-dir"]), `crossrefd-linux-${arch}`), "./cmd/crossrefdd"], { dryRun, env: { ...process.env, CGO_ENABLED: "0", GOOS: "linux", GOARCH: arch } });
  } else if (command === "extend") {
    if (!opts["base-inventory"] || !opts["base-output-dir"]) throw new Error("extend requires --base-inventory and --base-output-dir");
    const base = { inv: loadInventory(resolvePath(opts["base-inventory"])), out: resolvePath(opts["base-output-dir"]) };
    const plan = extensionPlan(base.inv, inv);
    if (!dryRun) { await prepare(inv, out, resolvePath(opts.binary), base); render(inv, out); }
    console.log(JSON.stringify({ dryRun, ...plan, note: "No SSH, deployments, or live funding transactions performed" }));
  } else if (command === "prepare") {
    if (dryRun) console.log(`Would prepare ${inv.nodes.length} independent keys and ${inv.chains.length} shared genesis files; no keys generated.`);
    else { await prepare(inv, out, resolvePath(opts.binary)); render(inv, out); }
  } else if (command === "doctor") {
    for (const host of operationHosts) await ssh(host, `set -eu\ncommand -v docker\ndocker version --format '{{.Server.Version}}'\ndocker compose version\ncommand -v tar\ncommand -v cmp\ncommand -v flock\ntest "$(uname -m)" = ${quote(host.architecture === "amd64" ? "x86_64" : "aarch64")}\nfree -m\ndf -h /\ntest "$(timedatectl show -p NTPSynchronized --value)" = yes`);
  } else if (command === "deploy") {
    if (!dryRun) { verifyPrepared(inv, out); render(inv, out); }
    for (const host of operationHosts) await deploy(inv, host, nodes, out, { ssh, dryRun, reuseBinaryFrom, buildDir: resolvePath(opts["build-dir"]) });
  } else if (["start", "stop", "restart", "status", "logs"].includes(command)) {
    if (command === "logs" && (!/^\d+$/.test(opts.tail) || Number(opts.tail) > 10000)) throw new Error("--tail must be 0..10000");
    const inspectArgs = command === "logs" ? ["logs", "--tail", opts.tail] : ["ps", "-a"];
    const operateHost = async host => {
      const services = nodes.filter((n) => n.hostId === host.id).map((n) => n.id);
      const args = command === "start" ? ["up", "-d", "--build"] : ["status", "logs"].includes(command) ? inspectArgs : [command];
      if (command === "restart") await waitHealthy(inv.nodes.filter((n) => n.hostId !== host.id));
      await dc(host, [...args, ...services]);
      if (command === "restart") await waitHealthy(inv.nodes);
    };
    if (opts["parallel-start"]) {
      // Bound administrative SSH load, then perform one shared health check.
      for (let i = 0; i < selectedHosts.length; i += 4) {
        const results = await Promise.allSettled(selectedHosts.slice(i, i + 4).map(operateHost));
        const failed = results.find(r => r.status === "rejected");
        if (failed) throw failed.reason;
      }
    } else {
      for (const host of selectedHosts) await operateHost(host);
    }
    if (relayerHost && ["status", "logs"].includes(command) && !opts.nodes && (!opts.host || opts.host === relayerHost.id)) await dc(relayerHost, [...inspectArgs, "relayer"]);
    if (command === "start") await waitHealthy(inv.nodes);
  } else if (command === "health") {
    console.log(JSON.stringify({ dryRun, ok: dryRun ? null : true, heights: await advancing(nodes), validatorsPerChain: inv.chains.map((c) => c.validators.length) }));
  } else if (command === "failover-test") {
    if (!opts.host || !nodes.length || opts.nodes) throw new Error("failover-test requires one BC --host and no --nodes");
    await advancing(inv.nodes);
    const host = hostFor(inv, opts.host), remaining = inv.nodes.filter((n) => n.hostId !== host.id);
    try {
      await dc(host, ["stop", ...nodes.map((n) => n.id)]);
      const heights = await advancing(remaining);
      console.log(JSON.stringify({ dryRun, ok: dryRun ? null : true, stoppedHost: host.id, survivingHeights: heights }));
    } finally {
      await dc(host, ["up", "-d", ...nodes.map((n) => n.id)]);
      await waitHealthy(inv.nodes);
    }
  } else if (command === "channels") {
    await advancing(inv.nodes);
    await dc(relayerHost, ["stop", "relayer"]);
    if (!dryRun) {
      // Discover OPEN channels from on-chain state; predicted sequence numbers are not authoritative.
      await hermes(["health-check"]);
      const actual = topology(inv);
      for (const pair of actual.pairs) {
        const source = inv.nodes.find((n) => n.domain === pair.left);
        const target = inv.nodes.find((n) => n.domain === pair.right);
        let existing = await discoverChannel(pair, source, target, queryChain);
        if (!existing) {
          await hermes(["create", "channel", "--a-chain", pair.leftChainId, "--b-chain", pair.rightChainId,
            "--a-port", "crossref", "--b-port", "crossref", "--channel-version", "crossref-1", "--order", "unordered", "--new-client-connection", "--yes"]);
          existing = await discoverChannel(pair, source, target, queryChain);
          if (!existing) throw new Error(`No verified OPEN channel for ${pair.left} -> ${pair.right}`);
        }
        pair.leftChannel = existing.left; pair.rightChannel = existing.right;
        for (const route of pair.routes) {
          route.fromChannel = route.from === pair.left ? existing.left : existing.right;
          route.toChannel = route.to === pair.left ? existing.left : existing.right;
        }
      }
      actual.channelsVerified = true;
      writeJSON(path.join(out, "topology.json"), actual);
    } else await hermes(["health-check"]);
  } else if (command === "relayer-start" || command === "relayer-stop") {
    if (command === "relayer-start" && !dryRun && !readJSON(path.join(out, "topology.json")).channelsVerified) throw new Error("Run channels before relayer-start");
    await dc(relayerHost, command === "relayer-start" ? ["up", "-d", "--force-recreate", "relayer"] : ["stop", "relayer"]);
  } else if (command === "relayer-refresh") {
    if (dryRun) {
      console.log("Would probe validator RPCs, select a healthy endpoint per chain, and replace only Hermes configuration.");
    } else {
      const endpoints = await selectRelayerEndpoints(inv, async (node) => { await snapshot([node]); return true; });
      await advancing(inv.nodes.filter((n) => endpoints.some((e) => e.service === n.id)));
      const wasRunning = (await dc(relayerHost, ["ps", "--status", "running", "--services"], true)).trim().split(/\s+/).includes("relayer");
      const file = path.join(out, "hosts", relayerHost.id, "hermes.toml");
      write(file, hermesConfig(inv, endpoints));
      const root = hostRoot(inv);
      await dc(relayerHost, ["stop", "relayer"]);
      await run(process.env.CROSSREF_SCP_BIN ?? "scp", ["-q", "-P", String(relayerHost.sshPort), "-i", relayerHost.identityFile,
        "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", file,
        `${relayerHost.sshUser}@${relayerHost.address}:${root}/hermes.next.toml`]);
      await ssh(relayerHost, `mv ${quote(`${root}/hermes.next.toml`)} ${quote(`${root}/hermes.toml`)}`);
      writeJSON(path.join(out, "relayer-endpoints.json"), { inventoryHash: fingerprint(inv), endpoints });
      // Recreate the container so its bind mount observes the atomically replaced config.
      if (wasRunning) await dc(relayerHost, ["up", "-d", "--no-deps", "--force-recreate", "relayer"]);
      console.log(JSON.stringify({ endpoints, restarted: wasRunning }));
    }
  } else if (command === "experiment") {
    if (!/^\d+$/.test(opts["checkpoint-height"]) || BigInt(opts["checkpoint-height"]) < 1n) throw new Error("invalid checkpoint height");
    if (!dryRun && !readJSON(path.join(out, "topology.json")).channelsVerified) throw new Error("Run channels before experiment");
    if (!dryRun) await dc(relayerHost, ["stop", "relayer"]);
    await run("bash", ["docker/scripts/run-crossref-experiment.sh"], { dryRun, env: { ...process.env,
      CROSSREF_REMOTE_INVENTORY: inventoryFile,
      CROSSREF_COMPOSE_BRIDGE: path.join(directory, "compose-bridge.mjs"),
      COMPOSE_FILE: path.join(out, "hosts", relayerHost.id, "compose.json"),
      TOPOLOGY_FILE: path.join(out, "topology.json"), CHAIN_COUNT: String(inv.chains.length), RELAYER_WORKER_COUNT: "1",
      DENOM: inv.denom, CHECKPOINT_HEIGHT: opts["checkpoint-height"], CROSSREF_RELAYER_COMMAND_MODE: "run",
      CROSSREF_RELAY_AFTER_BROADCAST: "one-shot", CROSSREF_START_RELAYER_BEFORE_BROADCAST: "0",
      CROSSREF_REQUIRE_CONSENSUS_PROOF: String(inv.requireConsensusProof), CROSSREF_CONSENSUS_PROOF_MODE: "cometbft",
      CROSSREF_METRICS_FILE: path.join(out, `experiment-h${opts["checkpoint-height"]}.jsonl`),
      CROSSREF_METRICS_SUMMARY_FILE: path.join(out, `experiment-h${opts["checkpoint-height"]}.summary.json`),
    } });
  }
}

export function verifiedBinaryCopy(source, target, digest) {
  if (!/^[a-f0-9]{64}$/.test(digest) || ![source, target].every((p) => /^\/[a-zA-Z0-9_/-]+$/.test(p))) throw new Error("Invalid binary reuse path/hash");
  return `set -eu\nif test -f ${quote(source)} && test "$(sha256sum ${quote(source)} | cut -d ' ' -f 1)" = ${quote(digest)}; then\n  cp ${quote(source)} ${quote(target)}\n  test "$(sha256sum ${quote(target)} | cut -d ' ' -f 1)" = ${quote(digest)}\n  printf REUSED\nelse\n  printf UPLOAD\nfi`;
}

async function deploy(inv, host, selected, out, { ssh, dryRun, reuseBinaryFrom, buildDir }) {
  const root = hostRoot(inv), source = path.join(out, "hosts", host.id);
  const incoming = `${root}/incoming-${randomUUID()}`;
  const scp = (file, target) => run(process.env.CROSSREF_SCP_BIN ?? "scp", ["-q", ...adminProxyOptions(host), "-P", String(host.sshPort), "-i", host.identityFile,
    "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=3", file, `${host.sshUser}@${host.address}:${target}`], { dryRun });
  console.log(`[deploy] ${host.id}: ${root}`);
  await ssh(host, `set -eu\numask 077\nmkdir -p ${quote(root)}\nchmod 700 ${quote(root)}\nmkdir ${quote(`${root}/.deployment-lock`)}\nmkdir -p ${quote(incoming)}`);
  try {
    await scp(path.join(source, "compose.json"), `${incoming}/compose.json`);
    if (host.id === inv.relayer?.host) {
      await scp(path.join(source, "hermes.toml"), `${incoming}/hermes.toml`);
      await scp(path.join(out, "prepared/relayer.mnemonic"), `${incoming}/relayer.mnemonic`);
      await ssh(host, `mkdir -p ${quote(`${root}/scripts`)} ${quote(`${root}/secrets`)} ${quote(`${root}/hermes-keys`)}\nif test -e ${quote(`${root}/secrets/relayer.mnemonic`)}; then cmp ${quote(`${root}/secrets/relayer.mnemonic`)} ${quote(`${incoming}/relayer.mnemonic`)}; fi`);
      for (const name of ["remote-hermes.sh", "hermes-one-shot.sh"]) await scp(path.join(source, "scripts", name), `${root}/scripts/${name}`);
      await ssh(host, `set -eu\nchmod 600 ${quote(`${incoming}/relayer.mnemonic`)}\nmv ${quote(`${incoming}/relayer.mnemonic`)} ${quote(`${root}/secrets/relayer.mnemonic`)}\nmv ${quote(`${incoming}/hermes.toml`)} ${quote(`${root}/hermes.toml`)}`);
    } else {
      for (const name of ["runtime.Dockerfile", "start-node.sh", ".dockerignore"]) await scp(path.join(source, name), `${root}/${name}`);
      const localBinary = path.join(buildDir, `crossrefd-linux-${host.architecture}`);
      const reused = reuseBinaryFrom ? await ssh(host, verifiedBinaryCopy(`${reuseBinaryFrom}/crossrefd`, `${incoming}/crossrefd`, hash(fs.readFileSync(localBinary))), true) : "";
      if (reused.trim() === "REUSED") console.log(`[deploy] ${host.id}: reused SHA256-verified binary`);
      else await scp(localBinary, `${incoming}/crossrefd`);
      const nodeIds = selected.filter((n) => n.hostId === host.id).map((n) => n.id);
      const archive = path.join(source, "homes.tar");
      if (!dryRun) await run("tar", ["-cf", archive, "-C", path.join(out, "prepared/homes"), ...nodeIds], { env: { ...process.env, COPYFILE_DISABLE: "1" } });
      try {
        if (!dryRun) fs.chmodSync(archive, 0o600);
        await scp(archive, `${incoming}/homes.tar`);
      } finally { if (!dryRun) fs.rmSync(archive, { force: true }); }
      await scp(path.join(directory, "install-home.sh"), `${incoming}/install-home.sh`);
      await ssh(host, `set -eu\numask 077\ntar -xf ${quote(`${incoming}/homes.tar`)} -C ${quote(incoming)}`);
      for (const id of nodeIds) await ssh(host, `sh ${quote(`${incoming}/install-home.sh`)} ${quote(root)} ${quote(id)} ${quote(incoming)}`);
      await ssh(host, `chmod 755 ${quote(`${incoming}/crossrefd`)} && mv ${quote(`${incoming}/crossrefd`)} ${quote(`${root}/crossrefd`)}`);
    }
    await ssh(host, `mv ${quote(`${incoming}/compose.json`)} ${quote(`${root}/compose.json`)}`);
  } finally {
    await ssh(host, `rm -rf ${quote(incoming)}\nrmdir ${quote(`${root}/.deployment-lock`)}`);
  }
}

export async function discoverChannel(pair, source, target, query, { port = "crossref", version = "crossref-1", verifyCounterpartyClient = false } = {}) {
  const response = await query(source, ["channel", "channels", "--limit", "1000"]);
  if (response.pagination?.next_key) throw new Error("Channel discovery was truncated; refusing to create duplicate channels");
  const channels = response.channels ?? [];
  for (const channel of channels) {
    if (channel.port_id !== port || channel.counterparty?.port_id !== port || channel.state !== "STATE_OPEN" || channel.ordering !== "ORDER_UNORDERED" || channel.version !== version || channel.connection_hops?.length !== 1) continue;
    const connection = (await query(source, ["connection", "end", channel.connection_hops[0]])).connection;
    if (connection?.state !== "STATE_OPEN") continue;
    const state = await query(source, ["client", "state", connection.client_id]);
    if ((state.client_state?.chain_id ?? state.identified_client_state?.client_state?.chain_id) !== pair.rightChainId) continue;
    const remote = (await query(target, ["channel", "end", port, channel.counterparty.channel_id])).channel;
    if (remote?.state === "STATE_OPEN" && remote.version === version && remote.ordering === "ORDER_UNORDERED" && remote.counterparty?.channel_id === channel.channel_id && remote.counterparty.port_id === port) {
      if (verifyCounterpartyClient) {
        if (remote.connection_hops?.length !== 1) continue;
        const reverse = (await query(target, ["connection", "end", remote.connection_hops[0]])).connection;
        if (reverse?.state !== "STATE_OPEN" || reverse.counterparty?.connection_id !== channel.connection_hops[0] ||
          connection.counterparty?.connection_id !== remote.connection_hops[0]) continue;
        const back = await query(target, ["client", "state", reverse.client_id]);
        if ((back.client_state?.chain_id ?? back.identified_client_state?.client_state?.chain_id) !== pair.leftChainId) continue;
      }
      return { left: channel.channel_id, right: channel.counterparty.channel_id };
    }
  }
  return null;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
