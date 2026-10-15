import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createEtcdAssignmentStore, createCoordinatedRelayers, validateCoordinationEndpoint, leaseWatchdogUnit } from "./relayer-coordinator.mjs";

const runId = "12345678-1234-4234-8234-123456789012";
const workers = [1, 2, 3].map(i => ({ name: `relayer-${i}`, leaseGuard: true }));
const plan = () => ({ strategy: "adaptive", owners: { "a/b": ["relayer-1"] },
  groups: [{ id: "a/b", channels: { a: "channel-0", b: "channel-0" } }],
  workers: workers.map((w, i) => ({ name: w.name, channels: i ? {} : { a: ["channel-0"], b: ["channel-0"] } })) });

function harness(fail = () => {}) {
  const calls = [], records = [];
  let epoch = 0;
  const store = { endpoint: "https://coordination.test",
    acquire: async () => { calls.push("acquire"); fail("acquire"); return { owner: runId }; },
    publish: async phase => { calls.push(phase); fail(phase); return { epoch: ++epoch }; },
    renew: async () => { calls.push("renew"); fail("renew"); },
    release: async () => { calls.push("release"); fail("release"); },
  };
  const request = async (w, op, fields) => {
    calls.push(`${w.name}:${op}`); fail(`${w.name}:${op}`, fields);
    if (op === "lease-capability") return { leaseGuard: true, workerName: w.name, endpoint: store.endpoint };
    return { stopped: true, configured: true, started: true };
  };
  const coordinator = createCoordinatedRelayers({ store, workers, request, record: event => records.push(event) });
  return { coordinator, calls, records, store, request };
}

test("all stops are confirmed before configuring or starting; idle workers stay stopped", async () => {
  const h = harness();
  const result = await h.coordinator.apply(plan());
  assert.equal(result.changed, true);
  assert.ok(h.calls.indexOf("relayer-3:stop") < h.calls.indexOf("active"));
  assert.ok(h.calls.indexOf("relayer-3:configure") < h.calls.indexOf("relayer-1:start"));
  assert.ok(!h.calls.includes("relayer-2:start") && !h.calls.includes("relayer-3:start"));
  const length = h.calls.length;
  assert.equal((await h.coordinator.apply(plan())).changed, false);
  assert.equal(h.calls.length, length);
  await h.coordinator.close();
  assert.equal(h.calls.at(-1), "release");
  assert.equal(h.records[0].type, "assignment-applied");
  const steps = h.records[0].stepTimings;
  assert.deepEqual(steps.map(s => `${s.worker}:${s.op}`), [
    "relayer-1:stop", "relayer-2:stop", "relayer-3:stop",
    "relayer-1:configure", "relayer-2:configure", "relayer-3:configure", "relayer-1:start",
  ]);
  assert.ok(steps.every(s => Number.isFinite(s.durationMs) && s.durationMs >= 0 && Date.parse(s.finishedAt) >= Date.parse(s.startedAt)));
  assert.ok(Date.parse(steps[2].finishedAt) <= Date.parse(steps[6].startedAt));
});

test("changing ownership pauses the lease and stops all workers before replacing filters", async () => {
  const h = harness(); await h.coordinator.apply(plan()); h.calls.length = 0;
  const next = plan(); next.workers[1].channels = next.workers[0].channels; next.workers[0].channels = {};
  next.owners["a/b"] = ["relayer-2"];
  await h.coordinator.apply(next);
  assert.equal(h.calls[0], "paused");
  assert.ok(h.calls.indexOf("relayer-3:stop") < h.calls.indexOf("relayer-2:start"));
});

test("a coordinator collision never stops or reconfigures another owner's workers", async () => {
  const h = harness(op => { if (op === "acquire") throw new Error("collision"); });
  await assert.rejects(h.coordinator.apply(plan()), /collision/);
  assert.ok(h.calls.every(op => !op.includes(":stop") && !op.includes(":configure") && !op.includes(":start")));
  assert.ok(!h.calls.includes("release"));
  assert.equal(h.coordinator.failed, true);
});

for (const failure of ["relayer-2:stop", "relayer-2:configure", "relayer-1:start", "active", "renew"]) {
  test(`${failure} failure releases ownership without unsafe continuation`, async () => {
    const h = harness(op => { if (op === failure) throw new Error("injected failure"); });
    await assert.rejects(h.coordinator.apply(plan()), /injected failure|stop unconfirmed/i);
    assert.equal(h.coordinator.failed, true);
    assert.equal(h.calls.at(-1), "release");
    if (["relayer-2:stop", "relayer-2:configure", "active", "renew"].includes(failure))
      assert.ok(!h.calls.includes("relayer-1:start"));
    await assert.rejects(h.coordinator.apply(plan()), /cannot automatically resume/);
  });
}

test("lost renewal stops every worker and forbids automatic restart", async () => {
  let lose = false;
  const h = harness(op => { if (lose && op === "renew") throw new Error("lease lost"); });
  await h.coordinator.apply(plan()); h.calls.length = 0; lose = true;
  await assert.rejects(h.coordinator.renew(), /lease lost/);
  assert.deepEqual(h.calls, ["renew", "relayer-1:stop", "relayer-2:stop", "relayer-3:stop", "release"]);
});

test("unverified watchdogs or mismatched coordinator endpoints cannot start workers", async () => {
  assert.throws(() => createCoordinatedRelayers({ store: {}, workers: workers.map(w => ({ ...w, leaseGuard: false })), request: async () => {} }), /watchdog/);
  const h = harness();
  const c = createCoordinatedRelayers({ store: h.store, workers, request: async w => ({ leaseGuard: true, workerName: w.name, endpoint: "https://wrong.test" }) });
  await assert.rejects(c.apply(plan()));
  assert.deepEqual(h.calls, []);
});

test("concurrent handovers and close during handover are rejected", async () => {
  const h = harness();
  let resume;
  const pending = new Promise(resolve => { resume = resolve; });
  h.store.acquire = async () => { await pending; return { owner: runId }; };
  const apply = h.coordinator.apply(plan());
  await assert.rejects(h.coordinator.apply(plan()), /Concurrent/);
  await assert.rejects(h.coordinator.close(), /in-flight/);
  resume(); await apply;
});

test("an uncertain stop during cleanup is not recorded as lease release", async () => {
  let cleanup = false;
  const h = harness(op => { if (cleanup && op === "relayer-3:stop") throw new Error("unreachable"); });
  await h.coordinator.apply(plan()); h.calls.length = 0; cleanup = true;
  await assert.rejects(h.coordinator.close(), /stop unconfirmed/i);
  assert.ok(!h.calls.includes("release"));
});

test("duplicate, missing or nonexclusive filters are rejected before any remote operation", async () => {
  for (const change of [p => { p.workers[1].channels = structuredClone(p.workers[0].channels); },
    p => { p.owners["a/b"] = ["relayer-1", "relayer-2"]; }, p => { delete p.workers[0].channels.b; }]) {
    const h = harness(), p = plan(); change(p);
    await assert.rejects(h.coordinator.apply(p));
    assert.deepEqual(h.calls, []);
  }
});

test("a late start response after lease loss triggers another confirmed stop", async () => {
  const h = harness();
  let entered, complete;
  const entering = new Promise(resolve => { entered = resolve; });
  const pending = new Promise(resolve => { complete = resolve; });
  const request = async (w, op, fields) => {
    if (op === "start") { entered(); await pending; }
    return h.request(w, op, fields);
  };
  const c = createCoordinatedRelayers({ store: h.store, workers, request });
  const applied = c.apply(plan()); await entering;
  h.store.renew = async () => { throw new Error("lost lease"); };
  await assert.rejects(c.renew(), /lost lease/);
  complete(); await assert.rejects(applied, /cannot automatically resume/);
  assert.ok(h.calls.lastIndexOf("relayer-3:stop") > h.calls.lastIndexOf("relayer-1:start"));
});

function gateway({ collision = false, loseResponse = false, stale = false, expire = false } = {}) {
  const calls = [];
  let value;
  const fetchImpl = async (url, options) => {
    const body = JSON.parse(options.body), op = url.split("/v3/")[1]; calls.push({ op, body });
    if (op === "lease/grant") return Response.json({ ID: "17", TTL: "30" });
    if (op === "lease/revoke") return Response.json({});
    if (op === "lease/keepalive") return new Response(`{"result":{"ID":"17","TTL":"${expire ? 0 : 30}"}}\n`, { status: 200 });
    if (op === "kv/range") return Response.json({ kvs: [{ value: stale ? "different" : value, lease: "17" }] });
    assert.equal(op, "kv/txn");
    if (!collision) value = body.success[0].request_put.value;
    if (loseResponse) throw new Error("response lost");
    return Response.json({ succeeded: !collision });
  };
  return { calls, fetchImpl };
}

test("gateway uses leases and CAS without exposing or overwriting another owner's key", async () => {
  const g = gateway(), s = createEtcdAssignmentStore({ endpoint: "https://coordination.test", runId, fetchImpl: g.fetchImpl });
  await s.acquire(plan()); await s.renew(); const active = await s.publish("active"); await s.release();
  assert.equal(active.phase, "active");
  assert.equal(g.calls[0].body.TTL, "30");
  const txns = g.calls.filter(c => c.op === "kv/txn");
  assert.equal(txns[0].body.compare[0].target, "VERSION");
  assert.equal(txns[1].body.compare[0].target, "VALUE");
  assert.equal(txns[1].body.success[0].request_put.lease, "17");
  assert.equal(g.calls.find(c => c.op === "kv/range").body.serializable, false);
});

test("lost mutation response is never retried and only the newly granted lease is revoked", async () => {
  for (const options of [{ loseResponse: true }, { collision: true }]) {
    const g = gateway(options), s = createEtcdAssignmentStore({ endpoint: "http://127.0.0.1:2379", runId, fetchImpl: g.fetchImpl });
    await assert.rejects(s.acquire(plan()));
    assert.equal(g.calls.filter(c => c.op === "kv/txn").length, 1);
    assert.deepEqual(g.calls.at(-1), { op: "lease/revoke", body: { ID: "17" } });
    await assert.rejects(s.renew(), /No coordination lease/);
  }
});

test("expired or externally replaced lease fails renewal", async () => {
  for (const option of [{ stale: true }, { expire: true }]) {
    const g = gateway(option), s = createEtcdAssignmentStore({ endpoint: "https://coordination.test", runId, fetchImpl: g.fetchImpl });
    await s.acquire(plan()); await assert.rejects(s.renew());
  }
});

test("cleanup verifies an expired lease and absent key without retrying revoke", async () => {
  const g = gateway(); let revokes = 0;
  const fetchImpl = async (url, options) => {
    if (url.endsWith("lease/revoke")) { revokes++; return Response.json({ error: "lease not found" }, { status: 404 }); }
    if (url.endsWith("lease/timetolive")) return Response.json({ ID: "17", TTL: "-1" });
    if (url.endsWith("kv/range")) {
      assert.equal(JSON.parse(options.body).serializable, false);
      return Response.json({});
    }
    return g.fetchImpl(url, options);
  };
  const store = createEtcdAssignmentStore({ endpoint: "http://127.0.0.1:28781", runId, fetchImpl });
  await store.acquire(plan()); await store.release(); await store.release();
  assert.equal(revokes, 1);
  await assert.rejects(store.renew(), /No coordination lease/);
});

test("cleanup retains uncertainty if lease or key exists or verification fails", async () => {
  for (const state of ["lease-alive", "key-present", "unreachable"]) {
    const g = gateway(); let revokes = 0;
    const fetchImpl = async (url, options) => {
      if (url.endsWith("lease/revoke")) { revokes++; throw new Error("response lost"); }
      if (url.endsWith("lease/timetolive")) {
        if (state === "unreachable") throw new Error("unreachable");
        return Response.json({ ID: "17", TTL: state === "lease-alive" ? "10" : "-1" });
      }
      if (url.endsWith("kv/range")) return Response.json({ kvs: [{ lease: "another-owner" }] });
      return g.fetchImpl(url, options);
    };
    const store = createEtcdAssignmentStore({ endpoint: "http://127.0.0.1:28781", runId, fetchImpl });
    await store.acquire(plan()); await assert.rejects(store.release());
    assert.equal(revokes, 1);
  }
});

test("renewal reads and CAS writes are serialized to avoid false ownership loss", async () => {
  const g = gateway();
  let resume, entered;
  const pending = new Promise(resolve => { resume = resolve; });
  const entering = new Promise(resolve => { entered = resolve; });
  const fetchImpl = async (url, options) => {
    if (url.endsWith("kv/range")) { entered(); await pending; }
    return g.fetchImpl(url, options);
  };
  const s = createEtcdAssignmentStore({ endpoint: "https://coordination.test", runId, fetchImpl });
  await s.acquire(plan());
  const renewal = s.renew(); await entering;
  const publish = s.publish("active");
  assert.equal(g.calls.filter(c => c.op === "kv/txn").length, 1);
  resume(); await renewal; await publish;
  assert.equal(g.calls.filter(c => c.op === "kv/txn").length, 2);
});

test("keepalive consumes chunked streaming responses without waiting for connection close", async () => {
  const g = gateway();
  const fetchImpl = (url, options) => url.endsWith("lease/keepalive") ? new Response(new ReadableStream({ start(controller) {
    controller.enqueue(new TextEncoder().encode('{"result":'));
    controller.enqueue(new TextEncoder().encode('{"ID":"17","TTL":"30"}}\n'));
  } })) : g.fetchImpl(url, options);
  const s = createEtcdAssignmentStore({ endpoint: "https://coordination.test", runId, fetchImpl });
  await s.acquire(plan()); await s.renew();
});

test("coordination endpoints reject credential URLs, nonlocal HTTP and paths", () => {
  for (const endpoint of ["http://remote.test", "https://user:pass@example.com", "https://example.com/path", "https://example.com/?token=x", "https://example.com/#x"])
    assert.throws(() => validateCoordinationEndpoint(endpoint));
  assert.equal(validateCoordinationEndpoint("https://example.com/"), "https://example.com");
  assert.equal(validateCoordinationEndpoint("http://[::1]:2379"), "http://[::1]:2379");
});

test("credentials are authenticated without being placed in URLs or data events", async t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "coord-auth-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const credentialsFile = path.join(root, "credentials.json");
  fs.writeFileSync(credentialsFile, JSON.stringify({ name: "controller", password: "private-test-value" }));
  const g = gateway();
  let logins = 0;
  const fetchImpl = async (url, options) => {
    assert.ok(!url.includes("private-test-value"));
    if (url.endsWith("auth/authenticate")) { logins++; return Response.json({ token: "private-test-token" }); }
    assert.equal(options.headers.Authorization, "private-test-token");
    assert.ok(!options.body.includes("private-test-value"));
    return g.fetchImpl(url, options);
  };
  const store = createEtcdAssignmentStore({ endpoint: "http://127.0.0.1:28781", credentialsFile, runId, fetchImpl });
  await store.acquire(plan()); await store.renew(); await store.release();
  assert.ok(logins >= 5);
});

test("watchdog death triggers a generation-scoped systemd stop without automatic restart", () => {
  const root = `/opt/crossref/multi/${runId}/r2`;
  const unit = leaseWatchdogUnit({ root, runId, workerName: "relayer-2" });
  assert.match(unit.content, new RegExp(`ExecStopPost=/usr/bin/python3 ${root}/vps-relayer-lease.py ${root}/gate.json --stop`));
  assert.match(unit.content, /Restart=no\n/);
  assert.match(unit.content, /KillMode=control-group\n/);
  assert.doesNotMatch(unit.content, /token|mnemonic|--key|\[Install\]/);
  assert.throws(() => leaseWatchdogUnit({ root: root + "/../other", runId, workerName: "relayer-2" }));
});
