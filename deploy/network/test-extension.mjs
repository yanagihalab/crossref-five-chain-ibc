import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { prepare, verifyPrepared } from "./orchestrator.mjs";
import { generate } from "./generate-inventory.mjs";
import { hash, readJSON, resolvePath, validateInventory, writeJSON } from "./lib.mjs";

function digestTree(root) {
  const files = {};
  function visit(dir) {
    for (const item of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, item.name);
      if (item.isDirectory()) visit(file);
      else files[path.relative(root, file)] = hash(fs.readFileSync(file));
    }
  }
  visit(root);
  return files;
}

test("real SDK extension preserves old identities and genesis, funds only new genesis, and is retry-safe", {
  skip: process.env.CROSSREF_NETWORK_INTEGRATION !== "1", timeout: 180000,
}, async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "crossref-extension-test-"));
  const binary = resolvePath(process.env.CROSSREF_BINARY ?? "build/crossrefdd");
  const before = validateInventory(generate({ chains: 1, network: "extension-test" }));
  const after = validateInventory(generate({ chains: 2, network: "extension-test" }));
  const oldOut = path.join(root, "original"), out = path.join(root, "expanded");
  fs.mkdirSync(oldOut);
  const base = { inv: before, out: oldOut };
  try {
    await prepare(before, oldOut, binary);
    const original = digestTree(oldOut);
    const oldManifest = verifyPrepared(before, oldOut);
    await assert.rejects(prepare(after, oldOut, binary, base), /separate output directory/);
    await assert.rejects(prepare(after, path.join(oldOut, "nested"), binary, base), /separate output directory/);
    const alias = path.join(root, "alias");
    fs.symlinkSync(oldOut, alias);
    await assert.rejects(prepare(after, alias, binary, base), /separate output directory/);
    const failedOut = path.join(root, "failed");
    await assert.rejects(prepare(after, failedOut, "/nonexistent-crossrefd", base), /ENOENT/);
    assert.equal(fs.existsSync(path.join(failedOut, "prepared")), false);
    assert.deepEqual(fs.readdirSync(failedOut), []);
    assert.deepEqual(digestTree(oldOut), original);

    await prepare(after, out, binary, base);
    const manifest = verifyPrepared(after, out);
    assert.deepEqual(digestTree(oldOut), original);
    assert.equal(manifest.genesisHashes[before.chains[0].id], oldManifest.genesisHashes[before.chains[0].id]);
    for (const node of before.nodes) {
      assert.deepEqual(manifest.nodes[node.id], oldManifest.nodes[node.id]);
      assert.deepEqual(digestTree(path.join(out, "prepared/homes", node.id)), digestTree(path.join(oldOut, "prepared/homes", node.id)));
    }
    assert.equal(new Set(Object.values(manifest.nodes).map((n) => n.consensusAddress)).size, 8);
    assert.equal(manifest.extension.addedNodeIds.length, 4);
    assert.deepEqual(manifest.extension.relayerFundingRequiredOnChains, [before.chains[0].id]);
    const oldGenesis = readJSON(path.join(out, "prepared/homes/chain-a-v1/config/genesis.json"));
    const newGenesis = readJSON(path.join(out, "prepared/homes/chain-b-v1/config/genesis.json"));
    assert.ok(!oldGenesis.app_state.bank.balances.some((b) => b.address === manifest.relayerAddress));
    assert.ok(newGenesis.app_state.bank.balances.some((b) => b.address === manifest.relayerAddress));
    const completed = digestTree(out);
    await prepare(after, out, binary, base);
    assert.deepEqual(digestTree(out), completed);

    const threeRaw = generate({ chains: 3, network: "extension-test" });
    threeRaw.hosts[8].address = threeRaw.hosts[8].bindAddress = "10.78.0.20";
    threeRaw.hosts[12] = after.hosts.find((h) => h.id === after.relayer.host);
    const three = validateInventory(threeRaw), thirdOut = path.join(root, "third");
    await prepare(three, thirdOut, binary, { inv: after, out });
    const third = verifyPrepared(three, thirdOut);
    assert.equal(third.relayerAddress, manifest.relayerAddress);
    assert.deepEqual(third.extension.relayerFundingRequiredOnChains, []);
    assert.equal(hash(fs.readFileSync(path.join(thirdOut, "prepared/relayer.mnemonic"))), hash(fs.readFileSync(path.join(out, "prepared/relayer.mnemonic"))));
    assert.deepEqual(digestTree(out), completed);
    const state = path.join(oldOut, "prepared/homes/chain-a-v1/data/priv_validator_state.json");
    writeJSON(state, { height: "20", round: 0, step: 0 });
    await assert.rejects(prepare(after, path.join(root, "signed-clone"), binary, base), /already signed/);
  } finally {
    // Only disposable identities created by this test are removed. No live keys are loaded.
    fs.rmSync(root, { recursive: true, force: true });
  }
});
