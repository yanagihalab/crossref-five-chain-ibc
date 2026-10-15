#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { validateInventory, writeJSON, resolvePath } from "./lib.mjs";

export function generate({ chains = 5, faults = 1, layout = "dedicated", network = "lab-202609", architecture = "amd64" } = {}) {
  if (!Number.isInteger(chains) || chains < 1 || chains > 32) throw new Error("chains must be 1..32 (1: consensus pilot, 2: IBC pilot, 3+: cross-reference)");
  if (!Number.isInteger(faults) || faults < 1 || faults > 3) throw new Error("faults must be 1..3");
  if (layout !== "dedicated") throw new Error("layout must be dedicated: one validator per server across all chains");
  const count = 3 * faults + 1, hosts = [], domains = [];
  const addHost = (id) => {
    const i = hosts.length;
    const address = `10.77.${Math.floor(i / 200)}.${11 + i % 200}`;
    hosts.push({ id, address, bindAddress: address, failureDomain: `physical-${id}`, architecture });
    return id;
  };
  for (let i = 0; i < chains; i++) {
    const suffix = i < 26 ? String.fromCharCode(97 + i) : String(i + 1);
    const domain = `chain-${suffix}`;
    const validators = Array.from({ length: count }, (_, n) => addHost(`${domain}-host-${n + 1}`));
    domains.push({ id: `${network}-${suffix}`, domain, validators });
  }
  const value = { version: 2, network, byzantineFaults: faults, remoteRoot: "/opt/crossref", denom: "stake",
    checkpointProofMaxLag: 10000, requireConsensusProof: false,
    ssh: { identityFile: "~/.ssh/id_ed25519" }, hosts, chains: domains,
    relayer: chains === 1 ? null : { host: addHost("relay-1"), image: "ghcr.io/informalsystems/hermes:1.10.5" } };
  validateInventory(value);
  return value;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const { values } = parseArgs({ options: { chains: { type: "string", default: "5" }, faults: { type: "string", default: "1" },
      layout: { type: "string", default: "dedicated" }, network: { type: "string", default: "lab-202609" },
      architecture: { type: "string", default: "amd64" }, output: { type: "string", default: "deploy/network/inventory.json" } } });
    const file = resolvePath(values.output);
    if (fs.existsSync(file)) throw new Error(`Refusing to overwrite ${file}`);
    writeJSON(file, generate({ ...values, chains: Number(values.chains), faults: Number(values.faults) }));
    console.log(`Created ${file}. Replace private addresses and verify failure domains before deployment.`);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
