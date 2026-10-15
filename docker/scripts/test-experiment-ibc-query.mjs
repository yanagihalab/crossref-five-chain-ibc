import assert from "node:assert/strict";
import fs from "node:fs";
import { spawnSync } from "node:child_process";

const script = fs.readFileSync(new URL("run-crossref-experiment.sh", import.meta.url), "utf8");
function definition(name) {
  const match = script.match(new RegExp(`^${name}\\(\\) \\{\\n[\\s\\S]*?^\\}`, "m"));
  assert(match, `missing ${name}`);
  return match[0];
}
function run(body, env = {}) {
  return spawnSync("bash", ["-c", ["set -euo pipefail", definition("client_for_source"), definition("wait_channel"), body].join("\n")], {
    encoding: "utf8", env: { ...process.env, ...env }, timeout: 10000,
  });
}
const mock = `
channel_id() { printf '%s\\n' channel-0; }
query_chain() {
  if [ "$4" = channel ]; then
    [ "$5:$6:$7" = end:crossref:channel-0 ] || return 9
    printf '%s\\n' "$CHANNEL_JSON"
  else
    [ "$4:$5:$6" = connection:end:connection-7 ] || return 9
    printf '%s\\n' "$CONNECTION_JSON"
  fi
}
client_for_source chain-a chain-b
`;
const channel = { channel: { state: "STATE_OPEN", connection_hops: ["connection-7"] } };
const connection = { connection: { state: "STATE_OPEN", client_id: "07-tendermint-12" } };
const baseEnv = { CHANNEL_JSON: JSON.stringify(channel), CONNECTION_JSON: JSON.stringify(connection) };
const valid = run(mock, baseEnv);
assert.equal(valid.status, 0, valid.stderr);
assert.equal(valid.stdout.trim(), "07-tendermint-12");
for (const state of ["STATE_INIT", "STATE_TRYOPEN", "STATE_CLOSED"]) {
  assert.notEqual(run(mock, { ...baseEnv, CHANNEL_JSON: JSON.stringify({ channel: { ...channel.channel, state } }) }).status, 0);
}
assert.notEqual(run(mock, { ...baseEnv, CHANNEL_JSON: JSON.stringify({ channel: { state: "STATE_OPEN", connection_hops: [] } }) }).status, 0);
assert.notEqual(run(mock, { ...baseEnv, CONNECTION_JSON: JSON.stringify({ connection: { state: "STATE_OPEN" } }) }).status, 0);
assert.notEqual(run(mock, { ...baseEnv, CHANNEL_JSON: "invalid-json" }).status, 0);
const waiting = run(`
phase=STATE_INIT
query_chain() { printf '{"channel":{"state":"%s"}}\\n' "$phase"; }
sleep() { phase=STATE_OPEN; }
CROSSREF_CHANNEL_WAIT_RETRIES=2
wait_channel chain-a channel-0 route-00 channel
[ "$phase" = STATE_OPEN ]
`);
assert.equal(waiting.status, 0, waiting.stderr);
const timeout = run(`
query_chain() { printf '{"channel":{"state":"STATE_TRYOPEN"}}\\n'; }
sleep() { :; }
CROSSREF_CHANNEL_WAIT_RETRIES=2
wait_channel chain-a channel-0 route-00 channel
`);
assert.notEqual(timeout.status, 0);
assert.match(timeout.stderr, /Timed out waiting for OPEN/);
console.log(JSON.stringify({ ok: true, cases: ["independent-client-counter", "non-open-channel", "missing-hop", "missing-client", "invalid-json", "wait-for-open", "bounded-timeout"] }));
