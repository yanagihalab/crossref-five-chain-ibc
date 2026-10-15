import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";
import { createTxPool, poolWorkId } from "./tx-pool.mjs";
import { createEtcdTxPoolBackend } from "./tx-pool-etcd.mjs";
import { createTimingRecorder } from "./timing-recorder.mjs";

const reads = new Set(["status", "health", "abci_info", "abci_query", "header", "block", "block_results", "commit", "validators", "tx", "tx_search", "block_search", "consensus_params"]);
const packetEvent = { received: "recv_packet", acknowledged: "acknowledge_packet", "timed-out": "timeout_packet" };
const digest = bytes => createHash("sha256").update(bytes).digest("hex").toUpperCase();

export function bindDecoded(config, chainId, worker, decoded) {
  assert.equal(decoded.address, config.accounts[worker]?.[chainId], "Signed account does not belong to this worker");
  assert.match(decoded.hash, /^[A-F0-9]{64}$/);
  assert.match(decoded.sequence, /^(0|[1-9][0-9]*)$/);
  assert.ok(decoded.packets.length <= 16);
  const works = decoded.packets.map(packet => {
    assert.equal(packet.signer, decoded.address);
    const route = config.routes.find(r => r.sourcePort === packet.sourcePort && r.sourceChannel === packet.sourceChannel &&
      r.destinationPort === packet.destinationPort && r.destinationChannel === packet.destinationChannel &&
      (packet.outcome === "received" ? r.destinationChain : r.sourceChain) === chainId);
    assert.ok(route, "Signed packet is outside verified channels");
    assert.ok(packet.action === "ack" ? packet.outcome === "acknowledged" : ["received", "timed-out"].includes(packet.outcome));
    return { ...route, sequence: packet.sequence, action: packet.action };
  });
  assert.equal(new Set(works.map(poolWorkId)).size, works.length);
  assert.equal(decoded.messageCount, works.length + decoded.clientUpdates);
  assert.ok(works.length || decoded.clientUpdates > 0);
  return works;
}

export function verifiedPacketEvents(works, packets, result) {
  const attributes = event => Object.fromEntries(event.attributes.map(a => [a.key, a.value]));
  return works.map((work, i) => {
    const expected = { packet_src_port: work.sourcePort, packet_src_channel: work.sourceChannel,
      packet_dst_port: work.destinationPort, packet_dst_channel: work.destinationChannel, packet_sequence: work.sequence };
    assert.ok(result.tx_result.events.some(event => {
      if (event.type !== packetEvent[packets[i].outcome]) return false;
      const actual = attributes(event);
      return Object.entries(expected).every(([key, value]) => actual[key] === value);
    }), "Confirmed transaction lacks the exact packet event");
    return { workId: poolWorkId(work), outcome: packets[i].outcome, txHash: result.hash.toUpperCase(), height: Number(result.height) };
  });
}

export function decodeWith(binary, input) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, [], { stdio: ["pipe", "pipe", "ignore"] });
    let output = "";
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    child.stdout.on("data", data => { output += data; if (output.length > 65536) child.kill("SIGKILL"); });
    child.stdin.on("error", () => {});
    child.once("error", () => { clearTimeout(timer); reject(new Error("Pool decoder unavailable")); });
    child.once("close", code => {
      clearTimeout(timer);
      try { assert.equal(code, 0); resolve(JSON.parse(output)); } catch { reject(new Error("Pool decoder rejected transaction")); }
    });
    child.stdin.end(JSON.stringify(input));
  });
}

export async function startRpcPool(config, { backend, decode = input => decodeWith(config.decoder, input), fetchImpl = fetch } = {}) {
  assert.equal(config.kind, "hermes-prebroadcast-tx-pool-v1");
  const timing = config.timingsFile ? createTimingRecorder(config.timingsFile, { runId: config.runId, component: "tx-pool" }) : null;
  const measure = (op, worker, chain, fn) => timing ? timing.measure(op, { node: worker, domain: chain }, fn) : fn();
  const log = event => fs.appendFileSync(config.eventsFile, JSON.stringify({ at: new Date().toISOString(), ...event }) + "\n", { mode: 0o600 });
  const pool = createTxPool({ ...config, backend: backend ?? createEtcdTxPoolBackend(config.etcd), record: log });
  if (!fs.existsSync(config.journalFile)) fs.writeFileSync(config.journalFile, "[]", { flag: "wx", mode: 0o600 });
  let journal = JSON.parse(fs.readFileSync(config.journalFile));
  assert.ok(journal.every(j => j.ticket.runId === config.runId));
  const save = () => {
    const persist = () => {
      const fd = fs.openSync(config.journalFile + ".tmp", "w", 0o600);
      try { fs.writeFileSync(fd, JSON.stringify(journal)); fs.fsyncSync(fd); }
      finally { fs.closeSync(fd); }
      fs.renameSync(config.journalFile + ".tmp", config.journalFile);
      const directory = fs.openSync(path.dirname(config.journalFile), "r");
      try { fs.fsyncSync(directory); } finally { fs.closeSync(directory); }
    };
    return timing ? timing.measureSync("journal.save", {}, persist) : persist();
  };
  const counters = Object.fromEntries(Object.keys(config.accounts).map(w => [w, { attempts: 0, suppressed: 0, broadcasts: 0, confirmed: 0, unknown: 0,
    packetMessages: 0, clientUpdates: 0, admissionMs: [], commitMs: [] }]));
  let fatal = journal.some(j => ["intent", "unknown", "failed-evidence"].includes(j.state)), closing = false, reconciling = false;
  const rpcRejections = {};
  const sending = new Set();
  const commitStarts = new Map();
  const jsonRpc = async (chain, method, params) => {
    const response = await fetchImpl(config.upstreams[chain], { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }), signal: AbortSignal.timeout(15000) });
    assert.ok(response.ok); const data = await response.json(); assert.ok(!data.error); return data.result;
  };
  const reconcile = async () => {
    if (reconciling) return;
    reconciling = true;
    try {
      for (const row of journal.filter(r => r.state === "intent" || r.state === "unknown")) {
        if (sending.has(row.ticket.batchId)) continue;
        let result;
        try { result = await measure("rpc.commit_lookup", row.ticket.worker, row.chainId,
          () => jsonRpc(row.chainId, "tx", { hash: Buffer.from(row.hash, "hex").toString("base64"), prove: false })); }
        catch { continue; } // tx-not-found never clears an intent or authorizes retransmission.
        try {
          assert.equal(result.hash.toUpperCase(), row.hash);
          assert.equal(digest(Buffer.from(result.tx, "base64")), row.hash);
          assert.equal(Number(result.tx_result.code ?? 0), 0);
          assert.ok(Number.isSafeInteger(Number(result.height)) && Number(result.height) > 0);
          const outcomes = verifiedPacketEvents(row.works, row.packets, result);
          await measure("store.resolve", row.ticket.worker, row.chainId, () => pool.resolve(row.ticket, null, async batch => ({ runId: config.runId, batchId: batch.batchId,
            transactions: [{ chainId: row.chainId, hash: row.hash, code: 0, height: Number(result.height) }], outcomes })));
          row.state = "confirmed"; save();
          const stats = counters[row.ticket.worker]; stats.confirmed++;
          if (commitStarts.has(row.ticket.batchId)) stats.commitMs.push(performance.now() - commitStarts.get(row.ticket.batchId));
          commitStarts.delete(row.ticket.batchId);
          log({ type: "rpc-confirmed", worker: row.ticket.worker, chainId: row.chainId, hash: row.hash, height: Number(result.height) });
        } catch {
          fatal = true; row.state = "failed-evidence"; save();
          log({ type: "rpc-failed-evidence", worker: row.ticket.worker, hash: row.hash });
        }
      }
    } finally { reconciling = false; }
  };
  const broadcast = async (worker, chainId, tx, request) => {
    assert.ok(!fatal && !closing, "Pool is not accepting transactions");
    const stats = counters[worker], start = performance.now(); stats.attempts++;
    const bytes = Buffer.from(tx, "base64");
    assert.ok(bytes.length > 0 && bytes.length <= 2097152 && bytes.toString("base64") === tx);
    const decoded = await measure("decoder.transaction", worker, chainId, () => decode({ tx })); assert.equal(decoded.hash, digest(bytes));
    const works = bindDecoded(config, chainId, worker, decoded);
    const reservation = await measure("store.reserve", worker, chainId, () => works.length ? pool.reserve(worker, works) : pool.reserveMaintenance(worker, chainId));
    if (!reservation.acquired) {
      stats.suppressed++; log({ type: "rpc-suppressed", worker, chainId, reason: reservation.reason, workIds: works.map(poolWorkId) });
      throw new Error("Pool work is owned or already confirmed");
    }
    const ticket = reservation.ticket;
    sending.add(ticket.batchId);
    let intent = false;
    try {
      const query = await measure("decoder.account_query", worker, chainId, () => decode({ address: decoded.address }));
      const response = await measure("rpc.account", worker, chainId, () => jsonRpc(chainId, "abci_query", { path: "/cosmos.auth.v1beta1.Query/Account", data: query.data, prove: false }));
      assert.equal(Number(response.response.code ?? 0), 0);
      const account = await measure("decoder.account_value", worker, chainId, () => decode({ address: decoded.address, accountValue: response.response.value }));
      assert.equal(account.sequence, decoded.sequence, "Stale signed account sequence");
      await measure("store.renew", worker, chainId, () => pool.renew(ticket));
      const row = { ticket, chainId, hash: decoded.hash, works, packets: decoded.packets, state: "intent", startedAt: Date.now() };
      journal.push(row); save();
      commitStarts.set(ticket.batchId, performance.now());
      intent = true;
      await measure("store.begin", worker, chainId, () => pool.begin(ticket, { workIds: works.map(poolWorkId), transactions: [{ chainId, hash: decoded.hash }] }));
      stats.admissionMs.push(performance.now() - start);
      // This is the sole network write. Never retry it, including after a lost response.
      stats.broadcasts++; stats.packetMessages += works.length; stats.clientUpdates += decoded.clientUpdates;
      log({ type: "rpc-broadcast", worker, chainId, hash: decoded.hash, packets: works.length, clientUpdates: decoded.clientUpdates,
        gasLimit: decoded.gasLimit, fee: decoded.fee });
      const responseBody = await measure("rpc.broadcast", worker, chainId, () => fetchImpl(config.upstreams[chainId], { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...request, params: { tx } }), signal: AbortSignal.timeout(15000) }));
      assert.ok(responseBody.ok);
      const result = await responseBody.json(); assert.ok(!result.error);
      assert.equal(Number(result.result.code ?? 0), 0, "CheckTx rejected the signed transaction");
      assert.equal(result.result.hash.toUpperCase(), decoded.hash);
      await measure("store.submitted", worker, chainId, () => pool.submitted(ticket));
      return result;
    } catch (error) {
      if (intent) {
        stats.unknown++; fatal = true;
        const row = journal.find(r => r.ticket.batchId === ticket.batchId); row.state = "unknown"; save();
        try { await pool.uncertain(ticket); } catch { /* Durable intent remains blocked even if the store is unavailable. */ }
        log({ type: "rpc-unknown", worker, chainId, hash: decoded.hash });
      } else await measure("store.release", worker, chainId, () => pool.release(ticket));
      throw error;
    } finally {
      sending.delete(ticket.batchId);
    }
  };
  const servers = [];
  for (const listener of config.listeners) {
    assert.ok(config.accounts[listener.worker]?.[listener.chainId]);
    assert.ok(Number.isSafeInteger(listener.port) && listener.port >= 28800 && listener.port <= 28899);
    const server = http.createServer(async (req, res) => {
      let id = null, method = "invalid";
      const reply = value => { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify(value)); };
      try {
        const url = new URL(req.url, "http://127.0.0.1");
        if (req.method === "GET" && url.pathname === "/pool-status") {
          reply({ runId: config.runId, fatal, pending: journal.filter(j => ["intent", "unknown", "failed-evidence"].includes(j.state)).length,
            counters, rpcRejections, timingDropped: timing?.dropped ?? null,
            phases: journal.map(j => ({ worker: j.ticket.worker, chainId: j.chainId, hash: j.hash, state: j.state })) }); return;
        }
        let request;
        if (req.method === "GET") request = { jsonrpc: "2.0", id: 1, method: url.pathname.slice(1), params: Object.fromEntries(url.searchParams) };
        else {
          assert.equal(req.method, "POST"); assert.equal(url.pathname, "/");
          let body = ""; for await (const part of req) { body += part; assert.ok(body.length <= 3 * 1024 * 1024); }
          request = JSON.parse(body);
        }
        assert.ok(request && !Array.isArray(request) && typeof request.method === "string");
        if (/^[a-z_]{1,40}$/.test(request.method)) method = request.method;
        id = request.id ?? 1;
        if (request.method === "broadcast_tx_sync") {
          assert.equal(req.method, "POST", "GET broadcasts are not enabled");
          assert.deepEqual(Object.keys(request.params), ["tx"]);
          reply(await broadcast(listener.worker, listener.chainId, request.params.tx, request));
        } else {
          assert.ok(reads.has(request.method), "RPC method is not permitted through pool");
          // Read requests are forwarded byte-for-byte, preserving GET parameter types.
          const response = await fetchImpl(config.upstreams[listener.chainId] + (req.method === "GET" ? req.url : ""), {
            method: req.method, ...(req.method === "POST" ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(request) } : {}),
            signal: AbortSignal.timeout(15000) });
          assert.ok(response.ok); reply(await response.json());
        }
      } catch {
        if (!Object.hasOwn(rpcRejections, method) && Object.keys(rpcRejections).length >= 32) method = "other";
        rpcRejections[method] = (rpcRejections[method] ?? 0) + 1;
        if (rpcRejections[method] === 1) log({ type: "rpc-request-rejected", method });
        reply({ jsonrpc: "2.0", id, error: { code: -32000, message: "Tx pool rejected request; inspect run-scoped pool status" } });
      }
    });
    server.on("upgrade", (req, socket) => socket.destroy());
    await new Promise((resolve, reject) => { server.once("error", reject); server.listen(listener.port, "127.0.0.1", resolve); });
    servers.push(server);
  }
  const timer = setInterval(() => { void reconcile(); }, 1000);
  log({ type: "rpc-pool-ready", runId: config.runId, listeners: servers.length });
  return { pool, broadcast, reconcile, counters, async close() {
    closing = true; clearInterval(timer);
    await Promise.all(servers.map(server => new Promise(resolve => server.close(resolve))));
    while (reconciling) await new Promise(resolve => setTimeout(resolve, 20));
    log({ type: "rpc-pool-stopped", runId: config.runId });
    timing?.close();
    if (config.timingsFile) fs.writeFileSync(path.join(path.dirname(config.timingsFile), "tx-pool-timing-final.json"),
      JSON.stringify({ runId: config.runId, timingDropped: timing.dropped, finishedAt: new Date().toISOString() }), { flag: "wx", mode: 0o600 });
  } };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
  const service = await startRpcPool(config);
  for (const signal of ["SIGTERM", "SIGINT"]) process.once(signal, async () => { await service.close(); process.exit(); });
}
