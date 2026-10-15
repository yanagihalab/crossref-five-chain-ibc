import assert from "node:assert/strict";
import fs from "node:fs";
import { randomUUID } from "node:crypto";
import { sameRelayerAssignments, validateExclusiveAssignments } from "./relayer-assignment.mjs";

const encode = value => Buffer.from(value).toString("base64");

export function leaseWatchdogUnit({ root, runId, workerName }) {
  assert.match(runId, /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/);
  assert.ok(["relayer-1", "relayer-2", "relayer-3"].includes(workerName));
  assert.ok(root.startsWith("/") && root.includes(runId) && /^[A-Za-z0-9/_.-]+$/.test(root) && !root.split("/").includes(".."));
  const name = `crossref-lease-${runId}-${workerName}.service`;
  return { name, content: `[Unit]\nDescription=Generation-scoped Hermes lease watchdog\nAfter=docker.service network-online.target\n\n[Service]\nType=simple\nUser=ubuntu\nWorkingDirectory=${root}\nExecStart=/usr/bin/python3 ${root}/vps-relayer-lease.py ${root}/gate.json\nExecStopPost=/usr/bin/python3 ${root}/vps-relayer-lease.py ${root}/gate.json --stop\nRestart=no\nKillMode=control-group\nTimeoutStopSec=90\nUMask=0077\nNoNewPrivileges=true\n\n` };
}

export function validateCoordinationEndpoint(endpoint) {
  const url = new URL(endpoint);
  assert.ok(!url.username && !url.password && !url.search && !url.hash && url.pathname === "/", "Invalid coordination endpoint");
  assert.ok(url.protocol === "https:" || (url.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)),
    "Coordination requires HTTPS except on loopback");
  return url.origin;
}

// Mutating requests are not retried: a lost response may have committed a write.
export function createEtcdAssignmentStore({ endpoint, runId, tokenFile, credentialsFile, ttlSeconds = 30, fetchImpl = fetch }) {
  endpoint = validateCoordinationEndpoint(endpoint);
  assert.match(runId, /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/);
  assert.ok(Number.isInteger(ttlSeconds) && ttlSeconds >= 15 && ttlSeconds <= 300);
  const key = `/crossref/relayers/${runId}/assignment`;
  const owner = randomUUID();
  let lease, current;
  let queue = Promise.resolve();
  const serialize = operation => (...args) => {
    const result = queue.then(() => operation(...args));
    queue = result.catch(() => {});
    return result;
  };
  const request = async (op, body) => {
    const headers = { "Content-Type": "application/json" };
    if (tokenFile) headers.Authorization = fs.readFileSync(tokenFile, "utf8").trim();
    if (credentialsFile) {
      assert.ok(!tokenFile, "Choose credentials or a token, not both");
      const credentials = JSON.parse(fs.readFileSync(credentialsFile, "utf8"));
      const authenticated = await fetchImpl(`${endpoint}/v3/auth/authenticate`, { method: "POST",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify(credentials), signal: AbortSignal.timeout(5000) });
      assert.ok(authenticated.ok, "Coordination authentication failed");
      const auth = await authenticated.json();
      assert.ok(!auth.error && typeof auth.token === "string" && auth.token.length > 0, "Coordination authentication failed");
      headers.Authorization = auth.token;
    }
    const response = await fetchImpl(`${endpoint}/v3/${op}`, { method: "POST", headers,
      body: JSON.stringify(body), signal: AbortSignal.timeout(5000) });
    assert.ok(response.ok, "Coordination request failed");
    // Keepalive is a streaming gateway response; consume only its first result.
    if (op === "lease/keepalive") {
      const reader = response.body.getReader();
      let value = "";
      try {
        const decoder = new TextDecoder();
        for (;;) {
          const part = await reader.read();
          if (part.done) throw new Error("Coordination keepalive ended without a result");
          value += decoder.decode(part.value, { stream: true });
          assert.ok(value.length <= 65536, "Coordination response too large");
          try { return JSON.parse(value.split("\n")[0]).result; } catch (e) { if (!(e instanceof SyntaxError)) throw e; }
        }
      } finally { await reader.cancel(); }
    }
    const result = await response.json();
    assert.ok(!result.error, "Coordination server rejected request");
    return result;
  };
  const verify = () => assert.ok(lease && current, "No coordination lease is held");
  return {
    key, endpoint,
    acquire: serialize(async plan => {
      assert.ok(!lease, "Coordination store already acquired");
      const granted = await request("lease/grant", { TTL: String(ttlSeconds) });
      assert.match(String(granted.ID), /^-?\d+$/);
      assert.notEqual(String(granted.ID), "0");
      lease = String(granted.ID);
      current = { runId, owner, epoch: 1, phase: "paused", plan: structuredClone(plan) };
      try {
        const result = await request("kv/txn", { compare: [{ key: encode(key), target: "VERSION", result: "EQUAL", version: "0" }],
          success: [{ request_put: { key: encode(key), value: encode(JSON.stringify(current)), lease } }] });
        assert.equal(result.succeeded, true, "Another coordinator owns this run; never replace it");
      } catch (error) {
        try { await request("lease/revoke", { ID: lease }); } catch { /* The unused lease expires without renewal. */ }
        lease = undefined; current = undefined;
        throw error;
      }
      return structuredClone(current);
    }),
    renew: serialize(async () => {
      verify();
      const result = await request("lease/keepalive", { ID: lease });
      assert.equal(String(result.ID), lease);
      assert.ok(Number(result.TTL) > 0, "Coordination lease expired");
      const value = await request("kv/range", { key: encode(key), serializable: false });
      assert.equal(value.kvs?.length, 1, "Coordination assignment disappeared");
      assert.equal(String(value.kvs[0].lease), lease, "Coordination lease attachment changed");
      assert.equal(value.kvs[0].value, encode(JSON.stringify(current)), "Coordination ownership changed");
    }),
    publish: serialize(async (phase, plan = current?.plan) => {
      verify();
      assert.ok(["paused", "active"].includes(phase));
      const value = { ...current, epoch: current.epoch + 1, phase, plan: structuredClone(plan) };
      const result = await request("kv/txn", { compare: [{ key: encode(key), target: "VALUE", result: "EQUAL", value: encode(JSON.stringify(current)) }],
        success: [{ request_put: { key: encode(key), value: encode(JSON.stringify(value)), lease } }] });
      assert.equal(result.succeeded, true, "Coordination ownership changed; stop all workers");
      current = value;
      return structuredClone(value);
    }),
    release: serialize(async () => {
      if (lease) {
        try { await request("lease/revoke", { ID: lease }); }
        catch (error) {
          // A slow worker shutdown can outlive its lease. Verify absence; never retry a write.
          const ttl = await request("lease/timetolive", { ID: lease });
          assert.equal(String(ttl.ID), lease);
          assert.equal(Number(ttl.TTL), -1, "Coordination lease release remains unconfirmed");
          const value = await request("kv/range", { key: encode(key), serializable: false });
          assert.equal(value.kvs?.length ?? 0, 0, "Coordination assignment remains after release");
        }
      }
      lease = undefined; current = undefined;
    }),
  };
}

export function createCoordinatedRelayers({ store, workers, request, record = () => {} }) {
  assert.equal(workers.length, 3);
  assert.equal(new Set(workers.map(w => w.name)).size, 3);
  assert.ok(workers.every(w => w.leaseGuard === true), "All workers require an independently verified lease watchdog");
  let held = false, everHeld = false, plan, epoch, owner, failed = false, applying = false, failureTask;
  const check = () => assert.equal(failed, false, "Failed coordination cannot automatically resume");
  const timedRequest = async (steps, worker, op, fields) => {
    const startedAt = new Date().toISOString(), started = performance.now();
    const result = await request(worker, op, fields);
    steps?.push({ worker: worker.name, op, startedAt, finishedAt: new Date().toISOString(), durationMs: performance.now() - started });
    return result;
  };
  const stopAll = async steps => {
    const errors = [];
    for (const worker of workers) {
      try { assert.equal((await timedRequest(steps, worker, "stop")).stopped, true); }
      catch { errors.push(worker.name); }
    }
    assert.equal(errors.length, 0, `Worker stop unconfirmed: ${errors.join(", ")}`);
  };
  const failClosed = async error => {
    failed = true;
    failureTask ??= (async () => {
      // An acquisition collision must not stop the existing owner's workers.
      if (held) {
        try { await stopAll(); } catch { /* Invalid leases also trigger worker-side stopping. */ }
        try { await store.release(); held = false; } catch { /* Never renew an uncertain lease. */ }
      }
      record({ type: "coordination-failed", message: error.message });
    })();
    await failureTask;
    throw error;
  };
  return {
    get failed() { return failed; },
    get ownsWorkers() { return everHeld; },
    halt() { failed = true; },
    async fail(error) { return failClosed(error); },
    async renew() {
      if (!held || failed) throw new Error("No active coordination ownership");
      try { await store.renew(); } catch (error) { await failClosed(error); }
    },
    async apply(next) {
      check();
      next = structuredClone(next);
      validateExclusiveAssignments(next);
      assert.equal(applying, false, "Concurrent handovers are forbidden");
      if (sameRelayerAssignments(plan, next)) return { changed: false, epoch };
      applying = true;
      const startedAt = Date.now();
      const stepTimings = [];
      try {
        if (!held) {
          for (const worker of workers) {
            const capability = await request(worker, "lease-capability");
            assert.equal(capability.leaseGuard, true);
            assert.equal(capability.workerName, worker.name);
            assert.equal(validateCoordinationEndpoint(capability.endpoint), store.endpoint);
          }
          owner = (await store.acquire(next)).owner; held = true; everHeld = true;
        }
        else await store.publish("paused");
        check();
        await stopAll(stepTimings);
        check();
        await store.renew();
        check();
        const active = await store.publish("active", next);
        check();
        epoch = active.epoch;
        for (const worker of workers) {
          const assignment = next.workers.find(w => w.name === worker.name);
          assert.ok(assignment);
          assert.equal((await timedRequest(stepTimings, worker, "configure", { channels: assignment.channels, epoch, owner })).configured, true);
          check();
        }
        for (const worker of workers) {
          if (Object.keys(next.workers.find(w => w.name === worker.name).channels).length) {
            await store.renew(); check();
            assert.equal((await timedRequest(stepTimings, worker, "start")).started, true);
            check();
          }
        }
        plan = next;
        record({ type: "assignment-applied", epoch, owners: next.owners, durationMs: Date.now() - startedAt, stepTimings });
        return { changed: true, epoch };
      } catch (error) {
        // A late start response may arrive after a concurrent renewal already failed.
        if (failed && everHeld) { try { await stopAll(); } catch { /* Worker watchdog remains the final stop guard. */ } }
        return await failClosed(error);
      }
      finally { applying = false; }
    },
    async close() {
      assert.equal(applying, false, "Wait for the in-flight handover before closing");
      if (!held) return;
      failed = true;
      await stopAll();
      if (held) await store.release();
      held = false;
    },
  };
}
