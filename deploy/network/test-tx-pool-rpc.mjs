import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createHash } from "node:crypto";
import { startRpcPool, bindDecoded, verifiedPacketEvents } from "./tx-pool-rpc.mjs";
import { createFixturePoolBackend, fixtureRunId, fixtureRoutes, fixtureAccounts, fixtureWork } from "./tx-pool-fixtures.mjs";

const work = fixtureWork(), chainId = work.destinationChain;
const config = () => ({ kind: "hermes-prebroadcast-tx-pool-v1", runId: fixtureRunId, routes: fixtureRoutes, accounts: fixtureAccounts, listeners: [], upstreams: { [chainId]: "http://127.0.0.1:1" } });
const decoded = worker => ({ hash: "A".repeat(64), address: fixtureAccounts[worker][chainId], sequence: "0", clientUpdates: 0, messageCount: 1,
  packets: [{ ...work, signer: fixtureAccounts[worker][chainId], outcome: "received" }] });

test("signed packet binding checks worker, target chain, counterparty and complete batch", () => {
  assert.deepEqual(bindDecoded(config(), chainId, "relayer-1", decoded("relayer-1")), [work]);
  for (const changes of [{ address: "wrong" }, { messageCount: 2 }, { packets: [{ ...decoded("relayer-1").packets[0], destinationChannel: "channel-99" }] }])
    assert.throws(() => bindDecoded(config(), chainId, "relayer-1", { ...decoded("relayer-1"), ...changes }));
  assert.throws(() => bindDecoded(config(), work.sourceChain, "relayer-1", decoded("relayer-1")));
});

test("evidence requires exact packet event, not merely code zero", () => {
  const d = decoded("relayer-1"), result = { hash: "A".repeat(64), height: "8", tx_result: { events: [] } };
  assert.throws(() => verifiedPacketEvents([work], d.packets, result));
  result.tx_result.events.push({ type: "recv_packet", attributes: Object.entries({ packet_src_port: work.sourcePort, packet_src_channel: work.sourceChannel,
    packet_dst_port: work.destinationPort, packet_dst_channel: work.destinationChannel, packet_sequence: work.sequence }).map(([key, value]) => ({ key, value })) });
  assert.equal(verifiedPacketEvents([work], d.packets, result)[0].height, 8);
});

test("three signed senders are suppressed before the only network write; independent evidence unlocks", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pool-rpc-test-")), backend = createFixturePoolBackend();
  const tx = Buffer.from("fixture envelope").toString("base64"), hash = createHash("sha256").update(Buffer.from(tx, "base64")).digest("hex").toUpperCase();
  let worker = "relayer-1", networkWrites = 0, committed = false;
  const decode = async input => input.address ? input.accountValue ? { sequence: "0" } : { data: "00" } : { ...decoded(worker), hash };
  const fetchImpl = async (url, options) => {
    const req = JSON.parse(options.body);
    if (req.method === "abci_query") return Response.json({ result: { response: { value: "fixture" } } });
    if (req.method === "broadcast_tx_sync") { networkWrites++; return Response.json({ result: { hash, code: 0 } }); }
    assert.equal(req.params.hash, Buffer.from(hash, "hex").toString("base64"));
    if (committed) return Response.json({ result: { hash, tx, height: "9", tx_result: { code: 0, events: [{ type: "recv_packet",
      attributes: Object.entries({ packet_src_port: work.sourcePort, packet_src_channel: work.sourceChannel,
        packet_dst_port: work.destinationPort, packet_dst_channel: work.destinationChannel, packet_sequence: work.sequence }).map(([key,value]) => ({key,value})) }] } } });
    return Response.json({ error: { message: "tx not found" } });
  };
  const service = await startRpcPool({ ...config(), journalFile: path.join(root, "journal.json"), eventsFile: path.join(root, "events.jsonl"),
    timingsFile: path.join(root, "tx-pool-operation-timings.jsonl") }, { backend, decode, fetchImpl });
  try {
    await service.broadcast(worker, chainId, tx, { jsonrpc: "2.0", id: 1, method: "broadcast_tx_sync" });
    for (worker of ["relayer-2", "relayer-3"]) await assert.rejects(service.broadcast(worker, chainId, tx, { method: "broadcast_tx_sync" }));
    assert.equal(networkWrites, 1);
    backend.advance(60000);
    await assert.rejects(service.broadcast(worker, chainId, tx, { method: "broadcast_tx_sync" }));
    assert.equal(networkWrites, 1);
    committed = true;
    await service.reconcile();
    assert.equal((await service.pool.status(work)).phase, "confirmed");
    assert.equal(service.counters["relayer-1"].confirmed, 1);
  } finally {
    await service.close();
    const text = fs.readFileSync(path.join(root, "tx-pool-operation-timings.jsonl"), "utf8"), rows = text.trim().split("\n").map(JSON.parse);
    assert.ok(rows.some(r => r.operation === "decoder.transaction"));
    assert.equal(rows.filter(r => r.operation === "rpc.broadcast" && r.outcome === "ok").length, 1);
    assert.ok(rows.some(r => r.operation === "store.resolve" && r.outcome === "ok"));
    assert.equal(JSON.parse(fs.readFileSync(path.join(root, "tx-pool-timing-final.json"))).timingDropped, 0);
    assert.ok(!text.includes(tx) && !text.includes("fixture envelope") && !text.includes("accountValue"));
    fs.rmSync(root, { recursive: true });
  }
});

test("maintenance reservations share account locks with packet work and cannot escape registered chains", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pool-rpc-test-"));
  const service = await startRpcPool({ ...config(), journalFile: path.join(root, "journal.json"), eventsFile: path.join(root, "events.jsonl") }, { backend: createFixturePoolBackend() });
  try {
    const result = await service.pool.reserveMaintenance("relayer-1", chainId); assert.equal(result.acquired, true);
    assert.equal((await service.pool.reserve("relayer-1", [work])).acquired, false);
    await assert.rejects(service.pool.reserveMaintenance("relayer-1", "outside"));
    await service.pool.release(result.ticket);
    assert.equal((await service.pool.reserve("relayer-1", [work])).acquired, true);
  } finally { await service.close(); fs.rmSync(root, { recursive: true }); }
});

test("Hermes latest committed header query is read-only and forwarded without weakening broadcast restrictions", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pool-rpc-test-")), calls = [];
  const service = await startRpcPool({ ...config(), listeners: [{ worker: "relayer-1", chainId, port: 28899 }],
    journalFile: path.join(root, "journal.json"), eventsFile: path.join(root, "events.jsonl") }, {
    backend: createFixturePoolBackend(), fetchImpl: async (url, options) => {
      const request = JSON.parse(options.body); calls.push(request);
      return Response.json({ jsonrpc: "2.0", id: request.id, result: { header: { height: "123" } } });
    },
  });
  try {
    assert.deepEqual(JSON.parse(fs.readFileSync(path.join(root, "journal.json"))), []);
    const request = { jsonrpc: "2.0", id: "fixture-id", method: "header", params: { height: "123" } };
    const value = await (await fetch("http://127.0.0.1:28899/", { method: "POST", body: JSON.stringify(request) })).json();
    assert.equal(value.result.header.height, "123"); assert.deepEqual(calls, [request]);
    for (const method of ["broadcast_tx_async", "broadcast_tx_commit"]) {
      const rejected = await (await fetch("http://127.0.0.1:28899/", { method: "POST", body: JSON.stringify({ ...request, method }) })).json();
      assert.equal(rejected.error.code, -32000);
    }
    assert.equal(calls.length, 1); assert.equal(service.counters["relayer-1"].broadcasts, 0);
  } finally { await service.close(); fs.rmSync(root, { recursive: true }); }
});
