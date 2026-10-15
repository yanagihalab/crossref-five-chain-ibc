import test from "node:test";
import assert from "node:assert/strict";
import { createTxPool, normalizePoolWork, poolWorkId } from "./tx-pool.mjs";
import { createEtcdTxPoolBackend } from "./tx-pool-etcd.mjs";
import { createTxPoolWorker } from "./tx-pool-worker.mjs";
import { assertTxPoolVpsReady, plannedHermesPacketCommand } from "./tx-pool-hermes.mjs";
import { createFixturePoolBackend, fixtureRunId, fixtureRoutes, fixtureAccounts, fixtureWorkers,
  fixtureWork, fixtureSender, verifyFixtureEvidence } from "./tx-pool-fixtures.mjs";

function harness(options = {}) {
  const backend = createFixturePoolBackend(), events = [], broadcasts = [];
  const pool = createTxPool({ backend, runId: fixtureRunId, routes: fixtureRoutes, accounts: fixtureAccounts,
    record: event => events.push(event), ...options });
  const worker = (name, senderOptions = {}) => createTxPoolWorker({ pool, worker: name,
    sender: fixtureSender(name, { calls: broadcasts, ...senderOptions }), verifyEvidence: verifyFixtureEvidence });
  return { backend, pool, events, broadcasts, worker };
}

async function intent(h, works = [fixtureWork()], worker = "relayer-1") {
  const { ticket } = await h.pool.reserve(worker, works);
  const sender = fixtureSender(worker), prepared = await sender.prepare(works);
  await h.pool.begin(ticket, prepared);
  return { ticket, sender, prepared };
}

test("three racing relayers prepare and broadcast one logical packet only once", async () => {
  const h = harness();
  const results = await Promise.all(fixtureWorkers.map(name => h.worker(name).execute([fixtureWork()])));
  assert.equal(results.filter(r => r.status === "confirmed").length, 1);
  assert.equal(results.filter(r => r.status === "suppressed").length, 2);
  assert.equal(h.broadcasts.length, 1);
  assert.equal(h.events.filter(e => e.type === "pool-reserved").length, 1);
});

test("reservations are shared across independent pool clients, not local mutexes", async () => {
  const h = harness(), other = createTxPool({ backend: h.backend, runId: fixtureRunId, routes: fixtureRoutes, accounts: fixtureAccounts });
  const results = await Promise.all([h.pool.reserve("relayer-1", [fixtureWork()]), other.reserve("relayer-2", [fixtureWork()])]);
  assert.equal(results.filter(r => r.acquired).length, 1);
});

test("unrelated packets with independent accounts may proceed concurrently", async () => {
  const h = harness();
  const results = await Promise.all(fixtureWorkers.map((name, i) => h.worker(name).execute([fixtureWork(i + 1)])));
  assert.ok(results.every(r => r.status === "confirmed")); assert.equal(h.broadcasts.length, 3);
});

test("a partially overlapping batch is rejected atomically without claiming its other packet", async () => {
  const h = harness();
  await h.pool.reserve("relayer-1", [fixtureWork(1), fixtureWork(2)]);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork(2), fixtureWork(3)])).acquired, false);
  assert.equal((await h.pool.reserve("relayer-3", [fixtureWork(3)])).acquired, true);
});

test("account sequence locks cover receive and possible timeout signing chains", async () => {
  const h = harness();
  await h.pool.reserve("relayer-1", [fixtureWork()]);
  assert.equal((await h.pool.reserve("relayer-1", [fixtureWork(2)])).acquired, false);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork(2)])).acquired, true);
});

test("expiry permits only pre-send takeover and fences the old worker", async () => {
  const h = harness(), { ticket } = await h.pool.reserve("relayer-1", [fixtureWork()]);
  const prepared = await fixtureSender("relayer-1").prepare([fixtureWork()]);
  h.backend.advance(30001);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).acquired, true);
  await assert.rejects(h.pool.begin(ticket, prepared), /fenced|expired/);
  await assert.rejects(h.pool.release(ticket), /fenced|expired/);
});

test("lease expiration between ownership read and intent CAS cannot authorize a send", async () => {
  const h = harness(), { ticket } = await h.pool.reserve("relayer-1", [fixtureWork()]);
  const original = h.backend.cas;
  h.backend.cas = args => { h.backend.advance(30001); return original(args); };
  await assert.rejects(h.pool.begin(ticket, await fixtureSender("relayer-1").prepare([fixtureWork()])), /CAS/);
  assert.equal((await h.pool.inspect(ticket)).phase, "reserved");
});

for (const phase of ["broadcast-intent", "submitted", "unknown"]) {
  test(`${phase} survives lease expiry and blocks duplicate packets and account reuse`, async () => {
    const h = harness(), { ticket } = await intent(h);
    if (phase === "submitted") await h.pool.submitted(ticket);
    if (phase === "unknown") await h.pool.uncertain(ticket);
    h.backend.advance(1000000);
    assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).reason, phase);
    assert.equal((await h.pool.reserve("relayer-1", [fixtureWork(2)])).reason, phase);
    await assert.rejects(h.pool.release(ticket));
  });
}

test("a changed wall clock does not release ownership; only backend leases do", async () => {
  let clock = "2026-10-05T00:00:00.000Z";
  const h = harness({ now: () => clock });
  await h.pool.reserve("relayer-1", [fixtureWork()]); clock = "2099-10-05T00:00:00.000Z";
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).acquired, false);
});

test("preparation failure releases the reservation but never invokes broadcast", async () => {
  const h = harness(); let sends = 0;
  const worker = createTxPoolWorker({ pool: h.pool, worker: "relayer-1", verifyEvidence: verifyFixtureEvidence,
    sender: { prepare: async () => { throw new Error("fixture"); }, broadcast: async () => { sends++; } } });
  await assert.rejects(worker.execute([fixtureWork()]), /nothing was broadcast/);
  assert.equal(sends, 0); assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).acquired, true);
});

test("broadcast ambiguity is durable, reports hashes and never automatically resends", async () => {
  const h = harness();
  await assert.rejects(h.worker("relayer-1", { failBroadcast: true }).execute([fixtureWork()]), /do not resend/);
  h.backend.advance(30001);
  assert.equal((await h.worker("relayer-2").execute([fixtureWork()])).status, "suppressed");
  assert.equal(h.broadcasts.length, 1);
  assert.equal(h.events.filter(e => e.type === "pool-broadcast-set")[0].transactions.length, 1);
});

test("a committed intent with lost response never broadcasts or releases ownership", async () => {
  const h = harness(); const original = h.pool.begin;
  h.pool.begin = (...args) => { h.backend.loseNextMutationResponse(); return original(...args); };
  await assert.rejects(h.worker("relayer-1").execute([fixtureWork()]), /do not resend/);
  assert.equal(h.broadcasts.length, 0);
  h.backend.advance(30001);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).acquired, false);
});

test("a lost reservation response is not retried and expires without sending", async () => {
  const h = harness(); h.backend.loseNextMutationResponse();
  await assert.rejects(h.worker("relayer-1").execute([fixtureWork()]));
  assert.equal(h.backend.calls.filter(c => c.op === "cas").length, 1); assert.equal(h.broadcasts.length, 0);
  h.backend.advance(30001);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).acquired, true);
});

test("resolution requires every exact prepared hash and an independent evidence callback", async () => {
  const h = harness(), { ticket, sender, prepared } = await intent(h);
  const evidence = await sender.broadcast(prepared);
  await assert.rejects(h.pool.resolve(ticket, evidence), /verifier/);
  const wrong = structuredClone(evidence); wrong.transactions[0].hash = "F".repeat(64);
  await assert.rejects(h.pool.resolve(ticket, wrong, verifyFixtureEvidence), /hashes/);
  await assert.rejects(h.pool.resolve(ticket, evidence, async () => { throw new Error("tx not found"); }), /not found/);
  assert.equal((await h.pool.inspect(ticket)).phase, "broadcast-intent");
  await h.pool.resolve(ticket, evidence, verifyFixtureEvidence);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).reason, "confirmed");
  assert.equal((await h.pool.reserve("relayer-1", [fixtureWork(2)])).acquired, true);
});

test("CheckTx or incomplete, wrong-chain and failed DeliverTx evidence cannot mark success", async () => {
  for (const mutate of [e => { delete e.transactions[0].height; }, e => { e.transactions[0].code = 1; },
    e => { e.outcomes = []; }, e => { e.outcomes[0].height = 99; }, e => { e.transactions[0].chainId = "chain-a-1"; }]) {
    const h = harness(), { ticket, sender, prepared } = await intent(h), evidence = await sender.broadcast(prepared);
    mutate(evidence); await assert.rejects(h.pool.resolve(ticket, evidence, verifyFixtureEvidence));
    assert.equal((await h.pool.inspect(ticket)).phase, "broadcast-intent");
  }
});

test("duplicate packet evidence cannot hide a missing member of a batch", async () => {
  const h = harness(), { ticket, sender, prepared } = await intent(h, [fixtureWork(1), fixtureWork(2)]);
  const evidence = await sender.broadcast(prepared); evidence.outcomes[1] = structuredClone(evidence.outcomes[0]);
  await assert.rejects(h.pool.resolve(ticket, evidence, verifyFixtureEvidence), /Duplicate/);
});

test("ACK work requires independently confirmed receive and has its own ownership", async () => {
  const h = harness();
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork(1, "ack")])).reason, "receive-not-confirmed");
  await h.worker("relayer-1").execute([fixtureWork()]);
  const results = await Promise.all(fixtureWorkers.map(name => h.worker(name).execute([fixtureWork(1, "ack")])));
  assert.equal(results.filter(r => r.status === "confirmed").length, 1); assert.equal(h.broadcasts.length, 2);
});

test("a confirmed timeout uses the same receive lock and never enables ACK", async () => {
  const h = harness(), { ticket } = await h.pool.reserve("relayer-1", [fixtureWork()]);
  const prepared = { workIds: [poolWorkId(fixtureWork())], transactions: [{ chainId: "chain-a-1", hash: "A".repeat(64) }] };
  await h.pool.begin(ticket, prepared);
  await h.pool.resolve(ticket, { transactions: [{ ...prepared.transactions[0], code: 0, height: 20 }],
    outcomes: [{ workId: prepared.workIds[0], outcome: "timed-out", txHash: "A".repeat(64), height: 20 }] }, verifyFixtureEvidence);
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork()])).reason, "confirmed");
  assert.equal((await h.pool.reserve("relayer-2", [fixtureWork(1, "ack")])).reason, "receive-not-confirmed");
});

test("pool restart shares persistent intent; a fresh run remains isolated", async () => {
  const h = harness(); await intent(h); h.backend.advance(30001);
  const restart = createTxPool({ backend: h.backend, runId: fixtureRunId, routes: fixtureRoutes, accounts: fixtureAccounts });
  assert.equal((await restart.reserve("relayer-2", [fixtureWork()])).acquired, false);
  const fresh = createTxPool({ backend: h.backend, runId: "20000000-0000-4000-8000-000000000001", routes: fixtureRoutes, accounts: fixtureAccounts });
  assert.equal((await fresh.reserve("relayer-2", [fixtureWork()])).acquired, true);
});

test("packet identifiers, counterparty registry, secrets and unsafe uint64 values are rejected", async () => {
  const h = harness();
  for (const work of [{ ...fixtureWork(), sequence: Number.MAX_SAFE_INTEGER + 1 }, { ...fixtureWork(), sequence: "18446744073709551616" },
    { ...fixtureWork(), sequence: "01" }, { ...fixtureWork(), sourceChain: "$(evil)" },
    { ...fixtureWork(), destinationChannel: "channel-99" }, { ...fixtureWork(), mnemonic: "not-allowed" }, { ...fixtureWork(), action: "timeout" }])
    await assert.rejects(h.pool.reserve("relayer-1", [work]));
  assert.equal(h.backend.calls.length, 0);
  assert.equal(normalizePoolWork(fixtureWork("18446744073709551615")).sequence, "18446744073709551615");
});

test("senders cannot add, omit or repeat work or omit pre-broadcast hashes", async () => {
  const h = harness(), { ticket } = await h.pool.reserve("relayer-1", [fixtureWork()]);
  for (const prepared of [{ workIds: [], transactions: [] }, { workIds: [poolWorkId(fixtureWork())], transactions: [] },
    { workIds: [poolWorkId(fixtureWork())], transactions: [{ chainId: "other", hash: "A".repeat(64) }] }])
    await assert.rejects(h.pool.begin(ticket, prepared));
  assert.equal((await h.pool.inspect(ticket)).phase, "reserved");
});

test("public events do not serialize sender contexts, keys or raw exceptions", async () => {
  const h = harness(); await h.worker("relayer-1").execute([fixtureWork()]);
  const text = JSON.stringify(h.events);
  assert.ok(!/privateContext|mnemonic|signedBytes|password|rawLog/.test(text));
});

test("peers can report owner, work and prepared hashes without credentials or a write", async () => {
  const h = harness();
  assert.equal((await h.pool.status(fixtureWork())).phase, "available");
  const { ticket, prepared } = await intent(h);
  await h.pool.uncertain(ticket); h.backend.advance(30001);
  const before = h.backend.calls.filter(c => c.op !== "read").length;
  const report = await h.pool.status(fixtureWork());
  assert.equal(report.worker, "relayer-1"); assert.equal(report.phase, "unknown");
  assert.deepEqual(report.transactions, prepared.transactions); assert.equal(report.reservationActive, false);
  assert.equal(h.backend.calls.filter(c => c.op !== "read").length, before);
  assert.ok(!/privateContext|lease|password|mnemonic/.test(JSON.stringify(report)));
});

test("a peer status report distinguishes an expired reservation from an uncertain broadcast", async () => {
  const h = harness(); await h.pool.reserve("relayer-1", [fixtureWork()]); h.backend.advance(30001);
  assert.equal((await h.pool.status(fixtureWork())).phase, "reservation-expired");
});

test("Hermes CLI planning flips the ACK endpoint and always limits to exactly one sequence", () => {
  const recv = plannedHermesPacketCommand(fixtureWork()), ack = plannedHermesPacketCommand(fixtureWork(1, "ack"));
  assert.equal(recv[1], "packet-recv"); assert.equal(ack[1], "packet-ack");
  assert.equal(ack[ack.indexOf("--src-chain") + 1], "chain-b-1");
  assert.equal(ack[ack.indexOf("--src-channel") + 1], "channel-2");
  assert.deepEqual(ack.slice(-2), ["--packet-sequences", "1"]);
  assert.throws(assertTxPoolVpsReady, /gated/);
});

const enc = value => Buffer.from(value).toString("base64");
test("etcd reads all keys in a single linearizable snapshot and uses revision CAS", async () => {
  const calls = [];
  const backend = createEtcdTxPoolBackend({ endpoint: "http://127.0.0.1:28781", fetchImpl: async (url, options) => {
    const body = JSON.parse(options.body); calls.push({ url, body });
    if (body.success?.[0]?.request_range) return Response.json({ succeeded: true, responses: body.success.map((op, index) => ({
      response_range: index ? {} : { kvs: [{ key: op.request_range.key, value: enc('{"phase":"reserved"}'), mod_revision: "90071992547409930", lease: "17" }] } })) });
    return Response.json({ succeeded: true });
  } });
  const rows = await backend.read(["/pool/a", "/pool/b"]);
  assert.equal(rows[0].revision, "90071992547409930");
  assert.equal(rows[1].revision, "0");
  assert.ok(calls[0].body.success.every(op => op.request_range.serializable === false));
  await backend.cas({ expected: rows, writes: [{ key: "/pool/b", value: { phase: "broadcast-intent" } }], deletes: ["/pool/a"] });
  assert.equal(calls[1].body.compare[0].target, "MOD");
  assert.equal(calls[1].body.compare[0].mod_revision, "90071992547409930");
  assert.equal(calls[1].body.success[0].request_put.lease, undefined);
});

test("etcd mutating requests are never retried and endpoints cannot leak credentials", async () => {
  let calls = 0;
  const backend = createEtcdTxPoolBackend({ endpoint: "https://pool.test", fetchImpl: async () => { calls++; throw new Error("response lost"); } });
  await assert.rejects(backend.cas({ expected: [{ key: "/a", revision: "0" }], writes: [{ key: "/a", value: {} }] }));
  assert.equal(calls, 1);
  for (const endpoint of ["http://remote.test", "https://user:password@pool.test", "https://pool.test/?token=secret"])
    assert.throws(() => createEtcdTxPoolBackend({ endpoint }));
});

test("etcd's omitted protobuf false is a CAS collision, not permission to send", async () => {
  const args = { expected: [{ key: "/a", revision: "0" }], writes: [{ key: "/a", value: {} }] };
  const backend = createEtcdTxPoolBackend({ endpoint: "http://127.0.0.1:28781",
    fetchImpl: async () => Response.json({ header: { revision: "15" } }) });
  assert.equal(await backend.cas(args), false);
  const malformed = createEtcdTxPoolBackend({ endpoint: "http://127.0.0.1:28781", fetchImpl: async () => Response.json({}) });
  await assert.rejects(malformed.cas(args), /uncertain/);
});

test("etcd consumes one streaming keepalive reply and cancels the stream", async () => {
  let canceled = false;
  const backend = createEtcdTxPoolBackend({ endpoint: "http://127.0.0.1:28781", fetchImpl: async () => new Response(new ReadableStream({
    start(controller) { controller.enqueue(new TextEncoder().encode('{"result":{"ID":"17","TTL":"30"}}\n')); },
    cancel() { canceled = true; },
  })) });
  assert.equal(await backend.keepAlive("17"), true); assert.equal(canceled, true);
});
