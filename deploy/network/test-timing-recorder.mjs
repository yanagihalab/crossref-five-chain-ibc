import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createTimingRecorder } from "./timing-recorder.mjs";

test("records monotonic async and sync durations without payloads", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "crossref-timing-"));
  try {
    const file = path.join(dir, "timings.jsonl");
    let ns = 0n;
    const timing = createTimingRecorder(file, { runId: "test-run", component: "controller", clock: () => ns,
      wallClock: () => "2026-09-29T00:00:00.000Z" });
    assert.equal(await timing.measure("rpc.block", { domain: "chain-a", height: 12 }, async () => { ns += 2500000n; return 7; }), 7);
    assert.throws(() => timing.measureSync("verify.signature", { round: 2 }, () => { ns += 1500000n; throw Error("secret payload"); }), /secret payload/);
    assert.throws(() => timing.measureSync("rpc.block", { rawUrl: "secret" }, () => true), /Unsupported timing label/);
    timing.close(); timing.close();
    const rows = fs.readFileSync(file, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(fs.statSync(file).mode & 0o777, 0o600);
    assert.deepEqual(rows.map(r => [r.operation, r.durationMs, r.outcome]),
      [["rpc.block", 2.5, "ok"], ["verify.signature", 1.5, "error"]]);
    assert.ok(!fs.readFileSync(file, "utf8").includes("secret"));
    assert.equal(rows[0].clock, "process-monotonic");
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
