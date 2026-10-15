import fs from "node:fs";
import path from "node:path";
import { fingerprint, readJSON, writeJSON } from "./lib.mjs";

export function validateTimeout(value) {
  if (typeof value !== "string" || !/^\d+(?:\.\d{1,3})?s$/.test(value) || Number(value.slice(0, -1)) < 1 || Number(value.slice(0, -1)) > 10) {
    throw new Error("timeout-commit must be 1..10 seconds, for example 5.5s");
  }
  return value;
}

export function readBlockTimeProfile(inv, out) {
  const file = path.join(out, "block-time-profile.json");
  if (!fs.existsSync(file)) return null;
  if (fs.statSync(file).size > 65536) throw new Error("Block-time profile too large");
  const profile = readJSON(file);
  if (profile.schemaVersion !== 1 || profile.inventoryHash !== fingerprint(inv)) throw new Error("Block-time profile inventory mismatch");
  validateTimeout(profile.timeoutCommit);
  return profile;
}

export function writeBlockTimeProfile(inv, out, timeoutCommit) {
  const profile = { schemaVersion: 1, inventoryHash: fingerprint(inv), timeoutCommit: validateTimeout(timeoutCommit), updatedAt: new Date().toISOString() };
  const file = path.join(out, "block-time-profile.json");
  writeJSON(`${file}.next`, profile);
  fs.renameSync(`${file}.next`, file);
  return profile;
}

const timestampNs = (value) => {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value ?? "");
  if (!m || !Number.isFinite(Date.parse(`${m[1]}Z`))) throw new Error("Invalid block timestamp");
  return BigInt(Date.parse(`${m[1]}Z`)) * 1000000n + BigInt((m[2] ?? "").padEnd(9, "0"));
};

export function blockTimeStats(headers, chainId) {
  if (headers.length < 2) throw new Error("At least two block headers required");
  const rows = [...headers].sort((a, b) => Number(BigInt(a.height) - BigInt(b.height)));
  const intervals = [];
  for (let i = 0; i < rows.length; i++) {
    if (rows[i].chain_id !== chainId || !/^[1-9]\d*$/.test(rows[i].height)) throw new Error("Block chain/height mismatch");
    if (!i) continue;
    if (BigInt(rows[i].height) !== BigInt(rows[i - 1].height) + 1n) throw new Error("Nonconsecutive block headers");
    const diff = timestampNs(rows[i].time) - timestampNs(rows[i - 1].time);
    if (diff <= 0n) throw new Error("Nonincreasing block timestamps");
    intervals.push(Number(diff) / 1e9);
  }
  const sorted = [...intervals].sort((a, b) => a - b), n = sorted.length;
  return { chainId, heightFrom: rows[0].height, heightTo: rows.at(-1).height, timeFrom: rows[0].time, timeTo: rows.at(-1).time,
    intervals: n, averageSeconds: intervals.reduce((a, b) => a + b, 0) / n,
    medianSeconds: (sorted[Math.floor((n - 1) / 2)] + sorted[Math.floor(n / 2)]) / 2,
    p95Seconds: sorted[Math.ceil(n * 0.95) - 1], minimumSeconds: sorted[0], maximumSeconds: sorted.at(-1), samplesSeconds: intervals };
}

export async function measureBlockTime(rpc, chainId, count = 100, height = null) {
  if (!Number.isSafeInteger(count) || count < 1 || count > 1000) throw new Error("interval count must be 1..1000");
  const status = await rpc("status");
  if (status?.node_info?.network !== chainId || status.sync_info?.catching_up !== false ||
      !(Math.abs(Date.now() - Date.parse(status.sync_info.latest_block_time)) < 60000)) throw new Error("RPC is wrong-chain, syncing, or stale");
  const end = BigInt(height ?? status.sync_info.latest_block_height), start = end - BigInt(count);
  if (start < 1n || end > BigInt(status.sync_info.latest_block_height)) throw new Error("Block range unavailable");
  const headers = [];
  for (let lo = start; lo <= end; lo += 20n) {
    const hi = lo + 19n > end ? end : lo + 19n;
    const result = await rpc(`blockchain?minHeight=${lo}&maxHeight=${hi}`);
    if (result?.block_metas?.length !== Number(hi - lo + 1n)) throw new Error("Incomplete block range");
    headers.push(...result.block_metas.map((b) => b.header));
  }
  const stats = blockTimeStats(headers, chainId);
  if (stats.heightFrom !== String(start) || stats.heightTo !== String(end)) throw new Error("Unexpected block range");
  return { ...stats, observedAt: new Date().toISOString() };
}
