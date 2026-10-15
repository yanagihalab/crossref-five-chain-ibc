import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { createHash } from "node:crypto";
import { isIP } from "node:net";
import { fileURLToPath } from "node:url";

export const directory = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(directory, "../..");
export const VPS_HERMES_TELEMETRY_PORT = 28779;
export const readJSON = (file) => JSON.parse(fs.readFileSync(file, "utf8"));
export const quote = (value) => `'${String(value).replaceAll("'", `'"'"'`)}'`;
export const hash = (value) => createHash("sha256").update(value).digest("hex");
export function sha256File(file) {
  const fd = fs.openSync(file, "r"), digest = createHash("sha256"), buffer = Buffer.allocUnsafe(64 * 1024);
  try {
    for (;;) { const size = fs.readSync(fd, buffer, 0, buffer.length, null); if (!size) break; digest.update(buffer.subarray(0, size)); }
    return digest.digest("hex");
  } finally { fs.closeSync(fd); }
}
export const fingerprint = (inventory) => hash(JSON.stringify(inventory));
export const resolvePath = (file) => path.resolve(repoRoot, file.replace(/^~\//, `${os.homedir()}/`));

export function write(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  fs.writeFileSync(file, value, { mode: 0o600 });
  fs.chmodSync(file, 0o600);
}

export function writeJSON(file, value) {
  write(file, `${JSON.stringify(value, null, 2)}\n`);
}

function identifier(value, label) {
  if (typeof value !== "string" || !/^[a-z][a-z0-9-]{0,47}$/.test(value)) {
    throw new Error(`${label}: expected a lowercase identifier (maximum 48 characters)`);
  }
  return value;
}

function integer(value, minimum, maximum, label) {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label}: invalid integer`);
  return value;
}

function unique(values, label) {
  if (new Set(values).size !== values.length) throw new Error(`duplicate ${label}`);
}

function address(value, label) {
  if (typeof value !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9.-]*$/.test(value)) throw new Error(`${label}: expected IPv4 address or DNS name`);
  return value;
}

function privateIP(value) {
  if (isIP(value) !== 4) return false;
  const [a, b] = value.split(".").map(Number);
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b <= 127);
}

export function validateInventory(raw) {
  if (raw.version !== 2) throw new Error("inventory.version must be 2");
  const network = identifier(raw.network, "network");
  const faults = integer(raw.byzantineFaults ?? 1, 1, 10, "byzantineFaults");
  if (!Array.isArray(raw.hosts) || !Array.isArray(raw.chains) || raw.chains.length < 1) {
    throw new Error("hosts and at least one chain are required");
  }
  const remoteRoot = raw.remoteRoot ?? "/opt/crossref";
  if (!/^\/[a-zA-Z0-9_/-]+$/.test(remoteRoot) || remoteRoot === "/") throw new Error("unsafe remoteRoot");
  const hosts = raw.hosts.map((h) => {
    const id = identifier(h.id, "host.id");
    const sshUser = h.sshUser ?? "ubuntu";
    if (!/^[a-z_][a-z0-9_-]*$/.test(sshUser)) throw new Error(`${id}: invalid sshUser`);
    if (!privateIP(h.bindAddress)) throw new Error(`${id}: bindAddress must be a private/VPN IPv4 address`);
    if (!["amd64", "arm64"].includes(h.architecture ?? "amd64")) throw new Error(`${id}: unsupported architecture`);
    return {
      id, address: address(h.address, `${id}.address`),
      endpoint: address(h.endpoint ?? h.bindAddress, `${id}.endpoint`), bindAddress: h.bindAddress,
      failureDomain: identifier(h.failureDomain, `${id}.failureDomain`),
      architecture: h.architecture ?? "amd64", sshUser,
      sshPort: integer(h.sshPort ?? 22, 1, 65535, `${id}.sshPort`),
      identityFile: resolvePath(h.identityFile ?? raw.ssh?.identityFile ?? "~/.ssh/id_ed25519"),
    };
  });
  unique(hosts.map((h) => h.id), "host ID");
  unique(hosts.map((h) => h.address), "SSH host address");
  unique(hosts.map((h) => h.endpoint), "host endpoint");
  unique(hosts.map((h) => h.bindAddress), "host bind address");
  const hostIds = new Set(hosts.map((h) => h.id));
  const validatorHosts = new Set();
  const validatorFailureDomains = new Set();
  const usedPorts = new Set();
  const nodes = [];
  const chains = raw.chains.map((c, chainIndex) => {
    const id = identifier(c.id, "chain.id");
    const domain = identifier(c.domain, `${id}.domain`);
    if (!Array.isArray(c.validators) || c.validators.length < 3 * faults + 1) {
      throw new Error(`${id}: at least ${3 * faults + 1} validators required for f=${faults}`);
    }
    unique(c.validators, `${id} validator host`);
    const domains = c.validators.map((hostId, validatorIndex) => {
      if (!hostIds.has(hostId)) throw new Error(`${id}: unknown host ${hostId}`);
      if (validatorHosts.has(hostId)) throw new Error(`${id}: duplicate validator host ${hostId}; one validator per server across all chains is required`);
      validatorHosts.add(hostId);
      const h = hosts.find((host) => host.id === hostId);
      if (validatorFailureDomains.has(h.failureDomain)) throw new Error(`${id}: duplicate validator failure domain ${h.failureDomain}; servers must be independent across all chains`);
      validatorFailureDomains.add(h.failureDomain);
      const base = integer((c.portBase ?? 26650 + chainIndex * 10), 1024, 65525, `${id}.portBase`);
      const ports = { p2p: base + 6, rpc: base + 7, grpc: base + 8, api: base + 9 };
      for (const port of Object.values(ports)) {
        const key = `${hostId}:${port}`;
        if (usedPorts.has(key)) throw new Error(`port collision: ${key}`);
        usedPorts.add(key);
      }
      nodes.push({ id: `${domain}-v${validatorIndex + 1}`, chainId: id, domain, hostId, ports });
      return h.failureDomain;
    });
    unique(domains, `${id} failure domain (validators must fail independently)`);
    return { id, domain, validators: [...c.validators] };
  });
  unique(chains.map((c) => c.id), "chain ID");
  unique(chains.map((c) => c.domain), "domain ID");
  unique(nodes.map((n) => n.id), "node ID");
  const relayerHost = raw.relayer?.host;
  if (raw.relayer != null || chains.length > 1) {
    if (!hostIds.has(relayerHost)) throw new Error("relayer.host must refer to a host (required for cross-reference)");
    const relayerDomain = hosts.find((h) => h.id === relayerHost).failureDomain;
    if (nodes.some((n) => n.hostId === relayerHost || hosts.find((h) => h.id === n.hostId).failureDomain === relayerDomain)) {
      throw new Error("Relayer must use a separate server/failure domain from all validators");
    }
  }
  if (hosts.some((h) => h.id !== relayerHost && !nodes.some((n) => n.hostId === h.id))) throw new Error("unused host in inventory");
  const denom = raw.denom ?? "stake";
  if (!/^[a-z][a-z0-9]{2,31}$/.test(denom)) throw new Error("invalid denom");
  if (typeof (raw.requireConsensusProof ?? false) !== "boolean") throw new Error("requireConsensusProof must be boolean");
  const image = raw.relayer?.image ?? "ghcr.io/informalsystems/hermes:1.10.5";
  if (!/^[a-zA-Z0-9./:_@-]+$/.test(image)) throw new Error("invalid relayer image");
  return {
    version: 2, network, remoteRoot, byzantineFaults: faults, hosts, chains, nodes, denom,
    checkpointProofMaxLag: integer(raw.checkpointProofMaxLag ?? 10000, 100, 1000000, "checkpointProofMaxLag"),
    requireConsensusProof: raw.requireConsensusProof ?? false,
    relayer: raw.relayer == null ? null : { host: relayerHost, image },
  };
}

export const loadInventory = (file) => validateInventory(readJSON(file));

export function extensionPlan(before, after) {
  for (const field of ["version", "network", "remoteRoot", "byzantineFaults", "denom", "checkpointProofMaxLag", "requireConsensusProof"]) {
    if (before[field] !== after[field]) throw new Error(`Extension cannot change existing ${field}`);
  }
  if (after.chains.length <= before.chains.length) throw new Error("Extension must append at least one chain");
  for (const field of ["hosts", "chains", "nodes"]) {
    for (const existing of before[field]) {
      const retained = after[field].find((value) => value.id === existing.id);
      if (JSON.stringify(retained) !== JSON.stringify(existing)) throw new Error(`Extension cannot change or remove existing ${field}: ${existing.id}`);
    }
  }
  if (before.chains.some((chain, i) => after.chains[i].id !== chain.id)) throw new Error("Extension must preserve existing chain order");
  if (before.relayer && JSON.stringify(before.relayer) !== JSON.stringify(after.relayer)) throw new Error("Extension cannot change an existing relayer");
  const addedChains = after.chains.filter((chain) => !before.chains.some((old) => old.id === chain.id));
  return {
    retainedChainIds: before.chains.map((chain) => chain.id),
    addedChainIds: addedChains.map((chain) => chain.id),
    addedNodeIds: after.nodes.filter((node) => addedChains.some((chain) => chain.id === node.chainId)).map((node) => node.id),
    // Live bank transactions are required; editing an existing genesis is never valid.
    relayerFundingRequiredOnChains: before.relayer ? [] : before.chains.map((chain) => chain.id),
  };
}
export const hostFor = (inv, id) => inv.hosts.find((h) => h.id === id);
export const hostRoot = (inv) => `${inv.remoteRoot}/${inv.network}`;
export const project = (inv, host) => `crossref-${inv.network}-${host.id}`;

export function topology(inv) {
  const chains = inv.chains.map((c) => {
    const node = inv.nodes.find((n) => n.chainId === c.id);
    const host = hostFor(inv, node.hostId);
    return { service: node.id, domain: c.domain, chainId: c.id, rpcPort: node.ports.rpc,
      rpcUrl: `http://${host.endpoint}:${node.ports.rpc}`, host: host.endpoint,
      grpcPort: node.ports.grpc, validators: inv.nodes.filter((n) => n.chainId === c.id).map((n) => n.id) };
  });
  const next = Object.fromEntries(chains.map((c) => [c.domain, 0]));
  const pairs = [];
  const routes = [];
  for (let i = 0; i < chains.length; i++) for (let j = i + 1; j < chains.length; j++) {
    const a = chains[i], b = chains[j];
    const ac = `channel-${next[a.domain]++}`, bc = `channel-${next[b.domain]++}`;
    const pair = { left: a.domain, right: b.domain, leftChainId: a.chainId, rightChainId: b.chainId, leftChannel: ac, rightChannel: bc };
    pair.routes = [[a, b, ac, bc], [b, a, bc, ac]].map(([from, to, fromChannel, toChannel]) => {
      const route = { id: `route-${String(routes.length).padStart(2, "0")}`, from: from.domain, to: to.domain,
        fromChainId: from.chainId, toChainId: to.chainId, fromChannel, toChannel, relayerWorker: 1 };
      routes.push(route);
      return route;
    });
    pairs.push(pair);
  }
  return { inventoryHash: fingerprint(inv), chainCount: chains.length, relayerCount: inv.relayer ? 1 : 0, chains, pairs, routes };
}

export async function selectRelayerEndpoints(inv, probe) {
  const endpoints = [];
  for (const chain of inv.chains) {
    let selected;
    for (const node of inv.nodes.filter((n) => n.chainId === chain.id)) {
      try { if (await probe(node)) { selected = node; break; } } catch { /* Try the next independent validator. */ }
    }
    if (!selected) throw new Error(`${chain.id}: no healthy RPC endpoint`);
    const host = hostFor(inv, selected.hostId);
    endpoints.push({ chainId: chain.id, service: selected.id, host: host.endpoint,
      rpcUrl: `http://${host.endpoint}:${selected.ports.rpc}`, grpcPort: selected.ports.grpc });
  }
  return endpoints;
}

export function hermesConfig(inv, endpoints = topology(inv).chains, { maxGas = 1500000, clearInterval = 100, gasMultiplier = 1.3 } = {}) {
  if (!inv.relayer) throw new Error("No relayer configured for this inventory");
  if (!Number.isSafeInteger(maxGas) || maxGas < 100000 || maxGas > 250000000) throw new Error("Invalid Hermes max gas");
  if (!Number.isSafeInteger(clearInterval) || clearInterval < 1 || clearInterval > 10000) throw new Error("Invalid Hermes clear interval");
  if (!Number.isFinite(gasMultiplier) || gasMultiplier < 1 || gasMultiplier > 10) throw new Error("Invalid Hermes gas multiplier");
  const header = `[global]
log_level = "info"
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
clear_interval = ${clearInterval}
clear_on_start = true
tx_confirmation = true
[rest]
enabled = false
host = "127.0.0.1"
port = 3000
[telemetry]
enabled = true
host = "0.0.0.0"
port = 3001
`;
  return header + endpoints.map((c) => `
[[chains]]
id = "${c.chainId}"
type = "CosmosSdk"
rpc_addr = "${c.rpcUrl}"
grpc_addr = "http://${c.host}:${c.grpcPort}"
event_source = { mode = "push", url = "${c.rpcUrl.replace(/^http:/, "ws:")}/websocket", batch_delay = "500ms" }
rpc_timeout = "30s"
account_prefix = "crossref"
key_name = "relayer"
store_prefix = "ibc"
default_gas = 100000
max_gas = ${maxGas}
gas_price = { price = 0.0, denom = "${inv.denom}" }
gas_multiplier = ${gasMultiplier}
# Keep client updates and packet execution in the same transaction when possible.
max_msg_num = 30
max_tx_size = 2097152
sequential_batch_tx = true
clock_drift = "10s"
max_block_time = "30s"
trusting_period = "336hours"
trust_threshold = { numerator = "1", denominator = "3" }
compat_mode = "0.37"
memo_prefix = "crossref-${inv.network}"
`).join("");
}

export function compose(inv, host) {
  const services = {};
  for (const n of inv.nodes.filter((node) => node.hostId === host.id)) {
    services[n.id] = {
      build: { context: ".", dockerfile: "runtime.Dockerfile" }, image: `crossref-${inv.network}:local`,
      restart: "unless-stopped", stop_grace_period: "60s",
      entrypoint: ["/bin/sh", "/opt/start-node.sh"],
      environment: { CHAIN_ID: n.chainId, DENOM: inv.denom },
      volumes: [`./homes/${n.id}:/var/crossref`],
      ports: Object.entries(n.ports).map(([kind, port]) => `${host.bindAddress}:${port}:${{ p2p: 26656, rpc: 26657, grpc: 9090, api: 1317 }[kind]}`),
      logging: { driver: "json-file", options: { "max-size": "10m", "max-file": "3" } },
      healthcheck: { test: ["CMD-SHELL", "curl -fsS http://localhost:26657/status | jq -e '.result.sync_info | (.latest_block_height | tonumber) > 0 and .catching_up == false' >/dev/null"], interval: "10s", timeout: "5s", retries: 30 },
    };
  }
  if (host.id === inv.relayer?.host) {
    services.relayer = {
      image: inv.relayer.image, user: "root", restart: "unless-stopped", stop_grace_period: "60s",
      entrypoint: ["/bin/sh", "/opt/crossref/scripts/remote-hermes.sh"], command: ["start"],
      environment: { CROSSREF_CHAIN_IDS: inv.chains.map((c) => c.id).join(" ") },
      volumes: ["./hermes.toml:/root/.hermes/config.toml:ro", "./secrets/relayer.mnemonic:/run/secrets/relayer.mnemonic:ro",
        "./scripts:/opt/crossref/scripts:ro", "./hermes-keys:/root/.hermes/keys"],
      ports: [`${host.bindAddress}:3001:3001`],
      logging: { driver: "json-file", options: { "max-size": "10m", "max-file": "3" } },
    };
  }
  return { name: project(inv, host), services };
}

export function render(inv, out) {
  fs.mkdirSync(out, { recursive: true, mode: 0o700 });
  const topologyFile = path.join(out, "topology.json");
  const previous = fs.existsSync(topologyFile) ? readJSON(topologyFile) : null;
  if (!previous?.channelsVerified || previous.inventoryHash !== fingerprint(inv)) writeJSON(topologyFile, topology(inv));
  writeJSON(path.join(out, "inventory.resolved.json"), inv);
  for (const host of inv.hosts) {
    const dir = path.join(out, "hosts", host.id);
    writeJSON(path.join(dir, "compose.json"), compose(inv, host));
    if (host.id === inv.relayer?.host) {
      const endpointFile = path.join(out, "relayer-endpoints.json");
      const saved = fs.existsSync(endpointFile) ? readJSON(endpointFile) : null;
      write(path.join(dir, "hermes.toml"), hermesConfig(inv, saved?.inventoryHash === fingerprint(inv) ? saved.endpoints : undefined));
      write(path.join(dir, "scripts/remote-hermes.sh"), fs.readFileSync(path.join(directory, "remote-hermes.sh")));
      write(path.join(dir, "scripts/hermes-one-shot.sh"), '#!/bin/sh\nexec /bin/sh /opt/crossref/scripts/remote-hermes.sh "$@"\n');
    } else {
      for (const name of ["runtime.Dockerfile", "start-node.sh"]) write(path.join(dir, name), fs.readFileSync(path.join(directory, name)));
      write(path.join(dir, ".dockerignore"), "homes/\nsecrets/\nhermes-keys/\nincoming-*\n*.tar\n.deployment-lock/\n");
    }
  }
}

export function sshArgs(host) {
  return [...adminProxyOptions(host), "-p", String(host.sshPort), "-i", host.identityFile, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
    "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=3", "-o", "ServerAliveInterval=15", `${host.sshUser}@${host.address}`];
}

export function adminProxyOptions(host) {
  const proxy = process.env.CROSSREF_ADMIN_PROXY;
  if (proxy) throw new Error("Historical administrative proxies are not supported in the public distribution");
  return [];
}

export function remoteCompose(inv, host, args) {
  return `cd ${quote(hostRoot(inv))} && docker compose -p ${quote(project(inv, host))} -f compose.json ${args.map(quote).join(" ")}`;
}
