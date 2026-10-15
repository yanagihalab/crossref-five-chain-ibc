#!/usr/bin/env node
import path from "node:path";
import { fileURLToPath } from "node:url";
import { loadInventory, hostFor, remoteCompose, sshArgs } from "./lib.mjs";
import { run } from "./orchestrator.mjs";

export function composeTarget(inv, args) {
  if (args[0] === "config" && args[1] === "--services" && args.length === 2) return { services: [...inv.nodes.map((n) => n.id), ...(inv.relayer ? ["relayer"] : [])] };
  if (!["exec", "run", "up", "stop"].includes(args[0])) throw new Error(`unsupported remote Compose operation: ${args[0]}`);
  const mapped = [...args];
  let i = 1;
  while (mapped[i]?.startsWith("-")) {
    const option = mapped[i];
    if (["-T", "--rm", "--no-deps", "-d"].includes(option)) i++;
    else if (["--entrypoint", "--index"].includes(option)) {
      if (!mapped[i + 1] || mapped[i + 1].startsWith("--")) throw new Error(`missing value for ${option}`);
      if (option === "--index" && mapped[i + 1] !== "1") throw new Error("remote deployment has one relayer service; index must be 1");
      i += 2;
    } else throw new Error(`unsupported remote Compose option: ${option}`);
  }
  const service = mapped[i];
  const node = inv.nodes.find((n) => n.id === service);
  const host = service === "relayer" ? hostFor(inv, inv.relayer?.host) : node && hostFor(inv, node.hostId);
  if (!host) throw new Error(`unknown remote service: ${service}`);
  if (["up", "stop"].includes(mapped[0]) && mapped.length !== i + 1) throw new Error("remote up/stop requires exactly one service");
  return { host, args: mapped };
}

export async function main(args = process.argv.slice(2)) {
  if (!process.env.CROSSREF_REMOTE_INVENTORY) throw new Error("CROSSREF_REMOTE_INVENTORY is required");
  const inv = loadInventory(process.env.CROSSREF_REMOTE_INVENTORY);
  const target = composeTarget(inv, args);
  if (target.services) { console.log(target.services.join("\n")); return; }
  await run(process.env.CROSSREF_SSH_BIN ?? "ssh", [...sshArgs(target.host), remoteCompose(inv, target.host, target.args)]);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
