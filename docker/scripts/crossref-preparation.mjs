import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

export function preparation(action, env = process.env) {
  assert.ok(["create", "consume"].includes(action));
  for (const key of ["TMPDIR", "TOPOLOGY_FILE", "CHECKPOINT_HEIGHT", "CROSSREF_RUN_ID"]) assert.ok(env[key], `${key} required`);
  const digest = (file) => createHash("sha256").update(fs.readFileSync(file)).digest("hex");
  const topology = JSON.parse(fs.readFileSync(env.TOPOLOGY_FILE, "utf8"));
  const state = { runId: env.CROSSREF_RUN_ID, height: env.CHECKPOINT_HEIGHT, topology: digest(env.TOPOLOGY_FILE),
    proofs: topology.chains.map((c) => {
      assert.match(c.domain, /^[a-zA-Z0-9_-]+$/);
      return digest(path.join(env.TMPDIR, `crossref-proof-${topology.chains.length}c-${c.domain}.json`));
    }) };
  const marker = path.join(env.TMPDIR, "crossref-preparation.json");
  if (action === "create") fs.writeFileSync(marker, JSON.stringify(state), { flag: "wx", mode: 0o600 });
  else {
    assert.deepEqual(JSON.parse(fs.readFileSync(marker, "utf8")), state, "Prepared run/proof identity changed; refusing broadcast");
    // Consume before any send; an interrupted delivery must not reissue source packets.
    fs.unlinkSync(marker);
  }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) preparation(process.argv[2]);
