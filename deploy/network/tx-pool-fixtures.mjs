import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { poolWorkId } from "./tx-pool.mjs";

// Deterministic test backend, not durable storage and never used for VPS delivery.
export function createFixturePoolBackend() {
  const rows = new Map(), leases = new Map();
  let revision = 0n, leaseNumber = 0, time = 0, loseNextResponse = false;
  const calls = [];
  const expire = () => {
    for (const [id, lease] of leases) if (lease.deadline <= time) {
      leases.delete(id);
      for (const [key, row] of rows) if (row.lease === id) rows.delete(key);
    }
  };
  return {
    kind: "memory-test-fixture", calls,
    advance(ms) { time += ms; expire(); },
    loseNextMutationResponse() { loseNextResponse = true; },
    async read(keys) {
      expire(); calls.push({ op: "read" });
      return keys.map(key => structuredClone(rows.get(key) ?? { key, revision: "0", value: null, lease: "0" }));
    },
    async cas({ expected, writes = [], deletes = [] }) {
      expire(); calls.push({ op: "cas", writes: writes.length, deletes: deletes.length });
      if (expected.some(row => (rows.get(row.key)?.revision ?? "0") !== row.revision)) return false;
      for (const row of writes) if (row.lease) assert.ok(leases.has(row.lease), "Cannot attach an expired lease");
      revision++;
      for (const row of writes) rows.set(row.key, { key: row.key, revision: String(revision), value: structuredClone(row.value), lease: row.lease ?? "0" });
      for (const key of deletes) rows.delete(key);
      if (loseNextResponse) { loseNextResponse = false; throw new Error("Fixture mutation committed but response was lost"); }
      return true;
    },
    async grant(ttl) {
      const id = String(++leaseNumber); leases.set(id, { ttl, deadline: time + ttl * 1000 });
      calls.push({ op: "grant" }); return id;
    },
    async keepAlive(id) {
      expire(); const lease = leases.get(id);
      if (!lease) return false;
      lease.deadline = time + lease.ttl * 1000; return true;
    },
    async revoke(id) { leases.delete(id); for (const [key, row] of rows) if (row.lease === id) rows.delete(key); },
  };
}

export const fixtureRunId = "10000000-0000-4000-8000-000000000001";
export const fixtureWorkers = ["relayer-1", "relayer-2", "relayer-3"];
export const fixtureRoutes = [["a", "b"], ["a", "d"], ["b", "a"], ["b", "d"], ["d", "a"], ["d", "b"]].map(([a, b], index) => ({
  sourceChain: `chain-${a}-1`, destinationChain: `chain-${b}-1`, sourcePort: "transfer", destinationPort: "transfer",
  sourceChannel: `channel-${index}`, destinationChannel: `channel-${[2, 4, 0, 5, 1, 3][index]}`,
}));
export const fixtureAccounts = Object.fromEntries(fixtureWorkers.map((worker, index) => [worker,
  Object.fromEntries(["a", "b", "d"].map(chain => [`chain-${chain}-1`, `cosmos1fixture${chain}worker${index + 1}`]))]));
export const fixtureWork = (sequence = 1, action = "receive", route = fixtureRoutes[0]) => ({ ...route, sequence: String(sequence), action });

export function fixtureSender(worker, { calls = [], failBroadcast = false } = {}) {
  return {
    async prepare(works) {
      const transactions = works.map(work => ({ chainId: work.action === "receive" ? work.destinationChain : work.sourceChain,
        hash: createHash("sha256").update(JSON.stringify([worker, work])).digest("hex").toUpperCase() }));
      return { workIds: works.map(poolWorkId), transactions, privateContext: { works: structuredClone(works) } };
    },
    async broadcast(prepared) {
      calls.push({ worker, workIds: prepared.workIds, transactions: prepared.transactions });
      if (failBroadcast) throw new Error("fixture network ambiguity");
      return { transactions: prepared.transactions.map(tx => ({ ...tx, height: 10, code: 0 })),
        outcomes: prepared.privateContext.works.map((work, index) => ({ workId: poolWorkId(work),
          outcome: work.action === "receive" ? "received" : "acknowledged", txHash: prepared.transactions[index].hash, height: 10 })) };
    },
  };
}

export async function verifyFixtureEvidence(batch, evidence) {
  return { ...structuredClone(evidence), runId: batch.runId, batchId: batch.batchId };
}
