import assert from "node:assert/strict";
import fs from "node:fs";
import { validateCoordinationEndpoint } from "./relayer-coordinator.mjs";

const encode = value => Buffer.from(value).toString("base64");

export function createEtcdTxPoolBackend({ endpoint, credentialsFile, tokenFile, fetchImpl = fetch, timeoutMs = 5000 }) {
  endpoint = validateCoordinationEndpoint(endpoint);
  assert.ok(!(credentialsFile && tokenFile), "Choose credentials or a token, not both");
  assert.ok(Number.isInteger(timeoutMs) && timeoutMs >= 1000 && timeoutMs <= 30000);
  const boundedJSON = async (response, stream = false) => {
    assert.ok(response.ok, "Tx pool request rejected");
    const reader = response.body.getReader(), decoder = new TextDecoder();
    let text = "";
    try {
      for (;;) {
        const part = await reader.read();
        text += decoder.decode(part.value ?? new Uint8Array(), { stream: !part.done });
        assert.ok(text.length <= 1024 * 1024, "Tx pool response too large");
        if (stream) {
          try { return JSON.parse(text.split("\n")[0]).result; }
          catch (error) { if (!(error instanceof SyntaxError)) throw error; }
        }
        if (part.done) break;
      }
      return JSON.parse(text);
    } finally { await reader.cancel(); }
  };
  const post = (op, body, headers) => fetchImpl(`${endpoint}/v3/${op}`, {
    method: "POST", headers: { "Content-Type": "application/json", ...headers },
    body: JSON.stringify(body), signal: AbortSignal.timeout(timeoutMs),
  });
  const request = async (op, body) => {
    const headers = {};
    if (tokenFile) headers.Authorization = fs.readFileSync(tokenFile, "utf8").trim();
    if (credentialsFile) {
      const credentials = JSON.parse(fs.readFileSync(credentialsFile, "utf8"));
      const auth = await boundedJSON(await post("auth/authenticate", credentials));
      assert.ok(!auth.error && typeof auth.token === "string" && auth.token.length > 0, "Tx pool authentication failed");
      headers.Authorization = auth.token;
    }
    const result = await boundedJSON(await post(op, body, headers), op === "lease/keepalive");
    assert.ok(result && !result.error, "Tx pool server rejected request");
    return result;
  };
  return {
    async read(keys) {
      assert.ok(keys.length > 0 && keys.length <= 128);
      const result = await request("kv/txn", { success: keys.map(key => ({ request_range: { key: encode(key), serializable: false } })) });
      assert.equal(result.responses?.length, keys.length);
      return keys.map((key, index) => {
        const kvs = result.responses[index].response_range.kvs ?? [];
        assert.ok(kvs.length <= 1);
        const kv = kvs[0];
        if (!kv) return { key, revision: "0", value: null, lease: "0" };
        assert.equal(kv.key, encode(key));
        assert.match(String(kv.mod_revision), /^[1-9][0-9]*$/);
        return { key, revision: String(kv.mod_revision), value: JSON.parse(Buffer.from(kv.value, "base64").toString("utf8")), lease: String(kv.lease ?? "0") };
      });
    },
    async cas({ expected, writes = [], deletes = [] }) {
      assert.ok(expected.length > 0 && expected.length <= 128);
      assert.equal(new Set(expected.map(row => row.key)).size, expected.length);
      const mutated = [...writes.map(row => row.key), ...deletes];
      assert.equal(new Set(mutated).size, mutated.length, "Duplicate mutation key");
      assert.ok(mutated.every(key => expected.some(row => row.key === key)), "Every mutation must be revision guarded");
      const result = await request("kv/txn", {
        compare: expected.map(row => ({ key: encode(row.key), target: "MOD", result: "EQUAL", mod_revision: row.revision })),
        success: [...writes.map(row => ({ request_put: { key: encode(row.key), value: encode(JSON.stringify(row.value)),
          ...(row.lease ? { lease: row.lease } : {}) } })), ...deletes.map(key => ({ request_delete_range: { key: encode(key) } }))],
      });
      // The protobuf JSON gateway may omit the default boolean false.
      if (result.succeeded === undefined) {
        assert.match(String(result.header?.revision), /^[1-9][0-9]*$/, "Malformed CAS response remains uncertain");
        assert.equal(result.responses?.length ?? 0, 0);
        return false;
      }
      assert.equal(typeof result.succeeded, "boolean");
      return result.succeeded;
    },
    async grant(ttlSeconds) {
      const result = await request("lease/grant", { TTL: String(ttlSeconds) });
      assert.match(String(result.ID), /^-?[1-9][0-9]*$/);
      assert.ok(Number(result.TTL) > 0);
      return String(result.ID);
    },
    async keepAlive(lease) {
      const result = await request("lease/keepalive", { ID: lease });
      assert.equal(String(result.ID), lease);
      return Number(result.TTL) > 0;
    },
    async revoke(lease) { await request("lease/revoke", { ID: lease }); },
  };
}
