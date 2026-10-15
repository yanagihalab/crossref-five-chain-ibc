import fs from "node:fs";
import assert from "node:assert/strict";

export const TIMING_SCHEMA_VERSION = 1;
const labelKeys = new Set(["domain", "node", "round", "route", "payload", "mode", "phase", "command", "endpoint", "height"]);

function labelsFor(values) {
  const labels = {};
  for (const [key, value] of Object.entries(values)) {
    assert.ok(labelKeys.has(key), `Unsupported timing label: ${key}`);
    if (value === undefined || value === null) continue;
    const text = String(value);
    assert.match(text, /^[A-Za-z0-9_.:/-]{1,100}$/, `Unsafe timing label: ${key}`);
    labels[key] = text;
  }
  return labels;
}

export function createTimingRecorder(file, { runId, component, clock = () => process.hrtime.bigint(),
  wallClock = () => new Date().toISOString(), onWriteError = () => {} } = {}) {
  assert.match(runId ?? "", /^[A-Za-z0-9-]{1,80}$/);
  assert.match(component ?? "", /^[a-z][a-z0-9-]{0,63}$/);
  if (fs.existsSync(file)) {
    const stat = fs.lstatSync(file);
    assert.ok(stat.isFile() && (stat.mode & 0o777) === 0o600, "Timing file must be regular with mode 0600");
  }
  const fd = fs.openSync(file, "a", 0o600);
  let closed = false, writeErrorReported = false, dropped = 0;
  const onFailure = () => {
    dropped++;
    if (!writeErrorReported) {
      writeErrorReported = true;
      try { onWriteError(); } catch { /* Timing diagnostics must not change experiment execution. */ }
    }
  };
  const write = (operation, labels, startedAt, start, outcome) => {
    const durationMs = Number(clock() - start) / 1e6;
    const row = { schemaVersion: TIMING_SCHEMA_VERSION, runId, component, operation, ...labels,
      startedAt, finishedAt: wallClock(), durationMs, outcome, clock: "process-monotonic" };
    try {
      const bytes = Buffer.from(`${JSON.stringify(row)}\n`);
      assert.equal(fs.writeSync(fd, bytes), bytes.length, "Short timing write");
    }
    catch { onFailure(); }
  };
  const begin = (operation, labels = {}) => {
    assert.match(operation, /^[a-z][a-z0-9_.-]{0,79}$/);
    return { labels: labelsFor(labels), startedAt: wallClock(), start: clock() };
  };
  return {
    async measure(operation, labels, fn) {
      const span = begin(operation, labels);
      let outcome = "ok";
      try { return await fn(); }
      catch (error) { outcome = "error"; throw error; }
      finally { write(operation, span.labels, span.startedAt, span.start, outcome); }
    },
    measureSync(operation, labels, fn) {
      const span = begin(operation, labels);
      let outcome = "ok";
      try { return fn(); }
      catch (error) { outcome = "error"; throw error; }
      finally { write(operation, span.labels, span.startedAt, span.start, outcome); }
    },
    get dropped() { return dropped; },
    close() { if (!closed) { closed = true; try { fs.closeSync(fd); } catch { onFailure(); } } },
  };
}
