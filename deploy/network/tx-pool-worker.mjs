import assert from "node:assert/strict";
import { poolWorkId } from "./tx-pool.mjs";

// prepare must not broadcast; its signed bytes stay exclusively inside the sender.
// broadcast must send only the immutable, pre-hashed set, without blind retries.
export function createTxPoolWorker({ pool, worker, sender, verifyEvidence, record = () => {} }) {
  assert.match(worker, /^relayer-[1-9][0-9]*$/);
  assert.equal(typeof sender.prepare, "function"); assert.equal(typeof sender.broadcast, "function");
  assert.equal(typeof verifyEvidence, "function");
  let running = false;
  const timed = async (operation, fn) => {
    const start = performance.now(); let ok = false;
    try { const result = await fn(); ok = true; return result; }
    finally { record({ type: "pool-operation", worker, operation, durationMs: performance.now() - start, ok }); }
  };
  return {
    async execute(works) {
      assert.equal(running, false, "One sender operation per worker at a time"); running = true;
      let ticket, intent = false;
      try {
        const reservation = await timed("reserve", () => pool.reserve(worker, works));
        if (!reservation.acquired) return { status: "suppressed", reason: reservation.reason };
        ticket = reservation.ticket;
        const prepared = await timed("prepare", () => sender.prepare(structuredClone(works)));
        assert.deepEqual([...prepared.workIds].sort(), works.map(poolWorkId).sort());
        await timed("renew", () => pool.renew(ticket));
        // A lost begin response may have committed: never release or send after it.
        intent = true;
        await timed("intent", () => pool.begin(ticket, { workIds: prepared.workIds, transactions: prepared.transactions }));
        const evidence = await timed("broadcast", () => sender.broadcast(prepared));
        await timed("submitted", () => pool.submitted(ticket));
        await timed("confirm", () => pool.resolve(ticket, evidence, verifyEvidence));
        return { status: "confirmed", ticket };
      } catch {
        if (ticket && !intent) {
          try { await timed("release", () => pool.release(ticket)); } catch { /* Expired pre-send leases fence this worker. */ }
        } else if (ticket) {
          try { await timed("unknown", () => pool.uncertain(ticket)); } catch { /* Durable intent remains blocking even if recording fails. */ }
        }
        // Raw sender errors can contain signed payloads or endpoint credentials.
        throw new Error(intent ? "Tx pool outcome unresolved; query prepared hashes, do not resend" : "Tx pool preparation failed; nothing was broadcast");
      } finally { running = false; }
    },
  };
}
