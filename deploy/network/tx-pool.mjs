import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";

const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
const routeFields = ["sourceChain", "destinationChain", "sourcePort", "sourceChannel", "destinationPort", "destinationChannel"];
const digest = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const clone = value => structuredClone(value);

export function normalizePoolRoute(value) {
  const result = {};
  for (const field of routeFields) {
    assert.equal(typeof value[field], "string", `Missing ${field}`);
    assert.match(value[field], field.endsWith("Channel") ? /^channel-(0|[1-9][0-9]{0,18})$/ : /^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,127}$/);
    result[field] = value[field];
  }
  assert.notEqual(result.sourceChain, result.destinationChain);
  return result;
}

export function normalizePoolWork(value) {
  assert.deepEqual(Object.keys(value).sort(), [...routeFields, "sequence", "action"].sort(), "Only public packet metadata is allowed");
  const result = normalizePoolRoute(value);
  assert.ok(["receive", "ack"].includes(value.action), "receive includes the mutually exclusive timeout operation");
  if (typeof value.sequence === "number") assert.ok(Number.isSafeInteger(value.sequence));
  assert.match(String(value.sequence), /^[1-9][0-9]{0,19}$/);
  assert.ok(BigInt(value.sequence) <= 18446744073709551615n, "Packet sequence exceeds uint64");
  return { ...result, sequence: String(value.sequence), action: value.action };
}

export function poolWorkId(value) {
  const work = normalizePoolWork(value);
  // Destination metadata is verified separately, not allowed to create an alias for the same packet.
  return digest([work.sourceChain, work.sourcePort, work.sourceChannel, work.sequence, work.action]);
}

function transactions(value, allowedChains) {
  assert.ok(Array.isArray(value) && value.length > 0 && value.length <= 32, "Prepared transaction hashes are required before broadcast intent");
  const result = value.map(tx => {
    assert.deepEqual(Object.keys(tx).sort(), ["chainId", "hash"]);
    assert.ok(allowedChains.includes(tx.chainId), "Transaction targets an unreserved account");
    assert.match(tx.hash, /^[a-fA-F0-9]{64}$/);
    return { chainId: tx.chainId, hash: tx.hash.toUpperCase() };
  });
  assert.equal(new Set(result.map(tx => `${tx.chainId}/${tx.hash}`)).size, result.length);
  return result.sort((a, b) => `${a.chainId}/${a.hash}`.localeCompare(`${b.chainId}/${b.hash}`));
}

// backend.read is linearizable; backend.cas atomically compares *all* read revisions.
// Leases cover reservations only. Broadcast intent and packet outcomes never expire.
export function createTxPool({ backend, runId, routes, accounts, ttlSeconds = 30, record = () => {}, now = () => new Date().toISOString() }) {
  assert.match(runId, UUID);
  assert.ok(Number.isInteger(ttlSeconds) && ttlSeconds >= 15 && ttlSeconds <= 300);
  const allowedRoutes = routes.map(normalizePoolRoute);
  accounts = clone(accounts);
  assert.ok(allowedRoutes.length > 0);
  const sourceRoute = route => JSON.stringify([route.sourceChain, route.sourcePort, route.sourceChannel]);
  assert.equal(new Set(allowedRoutes.map(sourceRoute)).size, allowedRoutes.length, "Source endpoints must identify one counterparty");
  assert.ok(Object.keys(accounts).length > 0);
  for (const [worker, addresses] of Object.entries(accounts)) {
    assert.match(worker, /^relayer-[1-9][0-9]*$/);
    for (const route of allowedRoutes) for (const chain of [route.sourceChain, route.destinationChain])
      assert.match(addresses[chain], /^[a-z0-9]{8,128}$/, "Account registry must contain public addresses, not keys");
  }
  const prefix = `/crossref/relayers/${runId}/tx-pool/`;
  const batchKey = id => `${prefix}batches/${id}`;
  const workKey = work => `${prefix}work/${poolWorkId(work)}`;
  const guardKey = key => `${prefix}guards/${key.slice(prefix.length)}`;
  const read = async keys => {
    const values = await backend.read(keys);
    assert.equal(values.length, keys.length);
    assert.deepEqual(values.map(v => v.key), keys);
    return values;
  };
  const emit = (type, batch, extra = {}) => {
    // Recorders cannot expose sender contexts or exception strings containing credentials.
    record({ type, runId, batchId: batch.batchId, worker: batch.worker, at: now(), ...extra });
  };
  const scope = work => {
    const route = allowedRoutes.find(route => sourceRoute(route) === sourceRoute(work));
    assert.ok(route, "Packet route is outside this run");
    assert.deepEqual(normalizePoolRoute(work), route, "Packet counterparty does not match the verified channel registry");
  };
  const ticketBatch = async ticket => {
    assert.equal(ticket.runId, runId); assert.match(ticket.batchId, UUID);
    const [entry] = await read([batchKey(ticket.batchId)]);
    assert.ok(entry.value, "Unknown pool batch");
    assert.equal(entry.value.worker, ticket.worker); assert.equal(entry.value.lease, ticket.lease);
    return entry;
  };
  const readBatch = async ticket => {
    const entry = await ticketBatch(ticket), batch = entry.value;
    const keys = [entry.key, ...batch.resources.flatMap(key => [key, guardKey(key)])];
    const rows = await read(keys);
    assert.deepEqual(rows[0].value, batch, "Batch changed during read");
    return { batch, rows };
  };
  const checkReserved = ({ batch, rows }) => {
    assert.equal(batch.phase, "reserved", "Only pre-broadcast reservations may expire or be released");
    for (let i = 1; i < rows.length; i += 2) {
      assert.equal(rows[i].value?.batchId, batch.batchId, "Reservation fenced by a new owner");
      assert.equal(rows[i].value.phase, "reserved");
      assert.equal(rows[i + 1].value?.batchId, batch.batchId, "Reservation lease expired");
      assert.equal(rows[i + 1].lease, batch.lease);
    }
  };
  const transition = async (state, phase, extra = {}) => {
    const batch = { ...state.batch, ...extra, phase, updatedAt: now() };
    const writes = [{ key: batchKey(batch.batchId), value: batch }];
    for (const key of batch.resources) {
      const old = state.rows.find(row => row.key === key);
      assert.equal(old.value?.batchId, batch.batchId, "Resource ownership changed");
      writes.push({ key, value: { ...old.value, phase } });
    }
    assert.equal(await backend.cas({ expected: state.rows, writes }), true, "Pool CAS lost; do not broadcast or retry the mutation");
    emit(`pool-${phase}`, batch);
    return clone(batch);
  };
  return {
    prefix,
    async status(rawWork) {
      const work = normalizePoolWork(rawWork); scope(work);
      const key = workKey(work);
      // Resolve the batch reference, then read the work and batch together in one snapshot.
      for (let count = 0; count < 3; count++) {
        const [reference] = await read([key]);
        if (!reference.value) return { runId, workId: poolWorkId(work), work, phase: "available", reservationActive: false };
        const rows = await read([key, guardKey(key), batchKey(reference.value.batchId)]);
        if (rows[0].value?.batchId !== reference.value.batchId) continue;
        const batch = rows[2].value; assert.ok(batch && batch.runId === runId, "Pool batch reference is inconsistent");
        const active = rows[1].value?.batchId === batch.batchId && rows[1].lease === batch.lease;
        return { runId, workId: poolWorkId(work), work, batchId: batch.batchId, worker: batch.worker,
          phase: batch.phase === "reserved" && !active ? "reservation-expired" : batch.phase,
          reservationActive: active, createdAt: batch.createdAt, updatedAt: batch.updatedAt,
          transactions: clone(batch.transactions ?? []),
          ...(rows[0].value.outcome ? { outcome: rows[0].value.outcome, height: rows[0].value.height } : {}) };
      }
      throw new Error("Pool changed during read; retry the read only");
    },
    async reserveMaintenance(worker, chainId) {
      return this.reserve(worker, [], { maintenanceChain: chainId });
    },
    async reserve(worker, rawWorks, options = {}) {
      assert.ok(Object.hasOwn(accounts, worker), "Unknown pool worker");
      assert.ok(Array.isArray(rawWorks) && rawWorks.length <= 16);
      assert.deepEqual(Object.keys(options).sort(), options.maintenanceChain ? ["maintenanceChain"] : []);
      if (options.maintenanceChain) {
        assert.equal(rawWorks.length, 0);
        assert.ok(allowedRoutes.some(r => [r.sourceChain, r.destinationChain].includes(options.maintenanceChain)));
      } else assert.ok(rawWorks.length > 0);
      const works = rawWorks.map(normalizePoolWork); works.forEach(scope);
      const workKeys = works.map(workKey);
      assert.equal(new Set(workKeys).size, workKeys.length, "Duplicate logical work in batch");
      const accountChains = options.maintenanceChain ? [options.maintenanceChain] :
        [...new Set(works.flatMap(w => w.action === "receive" ? [w.sourceChain, w.destinationChain] : [w.sourceChain]))].sort();
      const accountKeys = accountChains.map(chain => `${prefix}accounts/${digest([chain, accounts[worker][chain]])}`);
      const resources = [...workKeys, ...accountKeys];
      const dependencies = works.filter(w => w.action === "ack").map(w => workKey({ ...w, action: "receive" }));
      const keys = [...new Set([...resources.flatMap(key => [key, guardKey(key)]), ...dependencies])];
      const rows = await read(keys);
      for (const key of dependencies) {
        const previous = rows.find(row => row.key === key).value;
        if (previous?.phase !== "confirmed" || previous.outcome !== "received") return { acquired: false, reason: "receive-not-confirmed" };
      }
      for (const key of resources) {
        const row = rows.find(row => row.key === key), guard = rows.find(row => row.key === guardKey(key));
        if (row.value && row.value.phase !== "reserved") return { acquired: false, reason: row.value.phase };
        if (guard.value) return { acquired: false, reason: "reserved" };
      }
      const lease = await backend.grant(ttlSeconds);
      assert.match(lease, /^-?[1-9][0-9]*$/);
      const batchId = randomUUID();
      const batch = { runId, batchId, worker, lease, phase: "reserved", works, resources, accountChains, createdAt: now(), updatedAt: now() };
      const writes = [{ key: batchKey(batchId), value: batch }];
      for (let i = 0; i < resources.length; i++) {
        const key = resources[i];
        writes.push({ key, value: { batchId, phase: "reserved", ...(i < works.length ? { work: works[i] } : {}) } },
          { key: guardKey(key), value: { batchId }, lease });
      }
      // No write retry even if the server may have committed the reservation.
      const acquired = await backend.cas({ expected: [...rows, { key: batchKey(batchId), revision: "0", value: null, lease: "0" }], writes });
      if (!acquired) { await backend.revoke(lease); return { acquired: false, reason: "contention" }; }
      emit("pool-reserved", batch, { workIds: works.map(poolWorkId) });
      return { acquired: true, ticket: { runId, batchId, worker, lease } };
    },
    async renew(ticket) {
      const before = await readBatch(ticket); checkReserved(before);
      assert.ok(await backend.keepAlive(ticket.lease), "Pool lease expired");
      const state = await readBatch(ticket); checkReserved(state);
      emit("pool-renewed", state.batch);
    },
    async begin(ticket, prepared) {
      const state = await readBatch(ticket); checkReserved(state);
      assert.deepEqual([...prepared.workIds].sort(), state.batch.works.map(poolWorkId).sort(), "Signed batch must cover exactly the reserved work");
      const txs = transactions(prepared.transactions, state.batch.accountChains);
      const batch = await transition(state, "broadcast-intent", { transactions: txs });
      emit("pool-broadcast-set", batch, { transactions: txs });
      return batch;
    },
    async submitted(ticket) {
      const state = await readBatch(ticket);
      assert.equal(state.batch.phase, "broadcast-intent");
      return transition(state, "submitted");
    },
    async uncertain(ticket) {
      const state = await readBatch(ticket);
      assert.ok(["broadcast-intent", "submitted"].includes(state.batch.phase));
      return transition(state, "unknown");
    },
    async release(ticket) {
      const state = await readBatch(ticket); checkReserved(state);
      const batch = { ...state.batch, phase: "released", updatedAt: now() };
      assert.equal(await backend.cas({ expected: state.rows, writes: [{ key: batchKey(batch.batchId), value: batch }],
        deletes: state.batch.resources.flatMap(key => [key, guardKey(key)]) }), true, "Reservation release CAS lost");
      emit("pool-released", batch);
    },
    async resolve(ticket, evidence, verifyEvidence) {
      assert.equal(typeof verifyEvidence, "function", "Independent chain evidence verifier is required");
      const state = await readBatch(ticket), batch = state.batch;
      assert.ok(["broadcast-intent", "submitted", "unknown"].includes(batch.phase));
      // The verifier must query every prepared hash, never infer absence from tx-not-found.
      const verified = await verifyEvidence(clone(batch), evidence);
      assert.equal(verified.runId, runId);
      assert.equal(verified.batchId, batch.batchId);
      assert.ok(Array.isArray(verified.transactions));
      const txs = transactions(verified.transactions.map(tx => ({ chainId: tx.chainId, hash: tx.hash })), batch.accountChains);
      assert.deepEqual(txs, batch.transactions, "All prepared hashes must be independently accounted for");
      for (const tx of verified.transactions) {
        assert.equal(tx.code, 0, "DeliverTx failed; retain the intent for investigation");
        assert.ok(Number.isSafeInteger(tx.height) && tx.height > 0);
      }
      assert.equal(verified.outcomes.length, batch.works.length);
      const results = new Map();
      for (const outcome of verified.outcomes) {
        assert.ok(!results.has(outcome.workId), "Duplicate evidence cannot replace missing packet confirmation");
        results.set(outcome.workId, outcome);
      }
      const writes = [{ key: batchKey(batch.batchId), value: { ...batch, phase: "confirmed", updatedAt: now(),
        confirmations: verified.transactions.map(tx => ({ chainId: tx.chainId, hash: tx.hash.toUpperCase(), code: tx.code, height: tx.height })) } }];
      for (const work of batch.works) {
        const result = results.get(poolWorkId(work)); assert.ok(result, "Packet evidence missing");
        assert.ok((work.action === "ack" ? ["acknowledged"] : ["received", "timed-out"]).includes(result.outcome));
        const chainId = result.outcome === "received" ? work.destinationChain : work.sourceChain;
        const tx = verified.transactions.find(tx => tx.chainId === chainId && tx.hash.toUpperCase() === result.txHash?.toUpperCase());
        assert.ok(tx && result.height === tx.height, "Packet event must belong to an independently verified transaction");
        writes.push({ key: workKey(work), value: { batchId: batch.batchId, phase: "confirmed", work,
          outcome: result.outcome, txHash: tx.hash.toUpperCase(), chainId, height: tx.height } });
      }
      const accountKeys = batch.resources.filter(key => key.includes("/accounts/"));
      assert.equal(await backend.cas({ expected: state.rows, writes,
        deletes: [...accountKeys, ...batch.resources.map(guardKey)] }), true, "Evidence CAS lost; keep unresolved work blocked");
      emit("pool-confirmed", batch, { workIds: batch.works.map(poolWorkId) });
    },
    async inspect(ticket) { return clone((await ticketBatch(ticket)).value); },
  };
}
