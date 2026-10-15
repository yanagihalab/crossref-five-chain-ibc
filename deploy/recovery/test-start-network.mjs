import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { spawnSync } from 'node:child_process';
import { generate } from '../network/generate-inventory.mjs';
import { validateInventory, fingerprint } from '../network/lib.mjs';
import { auditHome, binaryHash, freshPreflight, validateTargets, startFresh, retiredAddressHashes } from './start-network.mjs';

const hash = b => createHash('sha256').update(b).digest('hex');
const files = { 'config/genesis.json': '{"chain_id":"fresh-test-a"}', 'config/priv_validator_key.json': '{"address":"fixture","priv_key":{"value":"fixture-not-a-real-key"}}',
  'config/node_key.json': 'node-fixture', 'config/config.toml': 'config-fixture', 'config/app.toml': 'app-fixture', 'data/priv_validator_state.json': '{"height":"0","round":0,"step":0}' };
function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'crossref-restart-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const inv = validateInventory(generate({ chains: 3, network: 'fresh-test' }));
  const manifest = { inventoryHash: fingerprint(inv), genesisHashes: {}, nodes: {} };
  for (const node of inv.nodes) {
    const home = path.join(dir, 'prepared/homes', node.id);
    for (const [name, contents] of Object.entries(files)) {
      const file = path.join(home, name); fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, contents);
    }
    manifest.genesisHashes[node.chainId] = hash(files['config/genesis.json']);
    manifest.nodes[node.id] = { consensusAddress: 'fixture', keyHashes: { 'priv_validator_key.json': hash(files['config/priv_validator_key.json']), 'node_key.json': hash(files['config/node_key.json']) } };
  }
  fs.writeFileSync(path.join(dir, 'prepared/manifest.json'), JSON.stringify(manifest));
  fs.writeFileSync(path.join(dir, 'prepared/relayer.mnemonic'), 'not-a-real-mnemonic');
  const buildDir = path.join(dir, 'build'); fs.mkdirSync(buildDir);
  for (const architecture of new Set(inv.hosts.map(h => h.architecture))) fs.writeFileSync(path.join(buildDir, 'crossrefd-linux-' + architecture), 'synthetic-binary');
  return { inv, output: dir, buildDir, inventory: path.join(dir, 'inventory.json'), confirmation: inv.network, multi: null };
}

test('offline audit preserves exact retained signing state without printing private keys', t => {
  const f = fixture(t), home = path.join(f.output, 'prepared/homes', f.inv.nodes[0].id);
  const expected = { chainId: 'fresh-test-a', signingHeight: '0', fileSHA256: Object.fromEntries(Object.entries(files).map(([n,b]) => [n,hash(b)])) };
  const before = fs.readFileSync(path.join(home, 'data/priv_validator_state.json'));
  const result = auditHome(home, expected);
  assert.equal(result.readOnly, true); assert.equal(result.nodeStarted, false);
  assert.ok(!JSON.stringify(result).includes('fixture-not-a-real-key'));
  assert.deepEqual(fs.readFileSync(path.join(home, 'data/priv_validator_state.json')), before);
  assert.throws(() => auditHome(home, { ...expected, signingHeight: '100' }));
  const file = path.join(home, 'config/app.toml'); fs.renameSync(file, file + '.retained'); fs.symlinkSync(file + '.retained', file);
  assert.throws(() => auditHome(home, expected));
});

test('retired IPs, generation names, and Relayer/Validator overlap are rejected before SSH', () => {
  const inv = validateInventory(generate({ chains: 3, network: 'fresh-test' }));
  assert.equal(retiredAddressHashes.size, 15);
  assert.ok([...retiredAddressHashes].every(value => /^[a-f0-9]{64}$/.test(value)));
  const address = '192.0.2.254', fingerprint = hash(address);
  retiredAddressHashes.add(fingerprint);
  try {
    const clone = structuredClone(inv); clone.hosts[0].address = address;
    assert.throws(() => validateTargets(clone, null));
  } finally { retiredAddressHashes.delete(fingerprint); }
  assert.throws(() => validateTargets({ ...inv, network: 'retained-network' }, null));
  const multi = { relayers: [1,2,3].map(i => ({ name: 'crossref-relayer-' + i, address: i === 1 ? inv.hosts.at(-1).address : inv.hosts[i].address })) };
  assert.throws(() => validateTargets(inv, multi));
});

test('starts only after all 13 preflights, block health, and channel verification; no init or provider commands', async t => {
  const f = fixture(t), calls = [];
  await startFresh(f, { execute: async (p,args) => { calls.push({ p,args }); return ''; } });
  assert.equal(calls.filter(c => c.p === 'ssh').length, 13);
  assert.equal(calls[0].p, 'make'); assert.deepEqual(calls[0].args, ['govulncheck']);
  assert.deepEqual(calls.filter(c => c.p === process.execPath).map(c => c.args[1]), ['doctor','start','health','channels','health','relayer-start']);
  const script = freshPreflight(f.inv, f.inv.nodes[0], f.output);
  assert.ok(script.includes('sha256sum') && script.includes('application.db') && script.includes('blockstore.db'));
  assert.equal(spawnSync('sh', ['-n'], { input: script }).status, 0);
  assert.ok(!/unsafe-reset|rm -|init |prune|provider/.test(calls.map(c => c.args.join(' ')).join('\n')));
});

test('wrong confirmation, signed template, or last-host failure prevents every start', async t => {
  const f = fixture(t), calls = [];
  const execute = async (p,args) => { calls.push({ p,args }); if(calls.length === 13) throw Error('preflight unavailable'); };
  await assert.rejects(startFresh({ ...f, confirmation: 'wrong' }, { execute })); assert.equal(calls.length, 0);
  await assert.rejects(startFresh(f, { execute })); assert.ok(calls.every(c => c.p === 'ssh' || c.p === 'make'));
  calls.length = 0;
  fs.writeFileSync(path.join(f.output, 'prepared/homes', f.inv.nodes[0].id, 'data/priv_validator_state.json'), '{"height":"1"}');
  await assert.rejects(startFresh(f, { execute })); assert.equal(calls.length, 0);
});

test('failed health never starts a Relayer and never performs an automatic rollback or retry', async t => {
  const f = fixture(t), commands = [];
  await assert.rejects(startFresh(f, { execute: async (p,args) => { if(p === 'ssh' || p === 'make') return ''; commands.push(args[1]); if(args[1] === 'health') throw Error('health failed'); } }));
  assert.deepEqual(commands, ['doctor','start','health']);
});

test('three Relayers are checked before Validator starts and started only after health and channels', async t => {
  const f = fixture(t), manifestFile = path.join(f.output, 'prepared/manifest.json');
  const manifest = JSON.parse(fs.readFileSync(manifestFile));
  manifest.relayers = [1,2,3].map(i => ({ name: 'relayer-' + i, address: 'funded-fixture-' + i }));
  fs.writeFileSync(manifestFile, JSON.stringify(manifest));
  f.multi = { relayers: [1,2,3].map(i => ({ name: 'crossref-relayer-' + i,
    address: i === 1 ? f.inv.hosts.at(-1).address : '192.0.2.' + i,
    hostConfigurationVerified: true, hermes: { ready: true } })) };
  const calls = [], deps = {
    execute: async (p,args) => { if (p !== 'make') calls.push(p === 'ssh' ? 'ssh' : args[1]); },
    relayAction: async (config, action) => { calls.push('relayer-' + action); return { ok: true }; },
  };
  const result = await startFresh(f, deps);
  assert.equal(result.relayers, 3); assert.equal(calls[0], 'relayer-check');
  assert.deepEqual(calls.slice(-6), ['doctor','start','health','channels','health','relayer-start']);
  calls.length = 0; f.multi.relayers[2].hermes.ready = false;
  await assert.rejects(startFresh(f, deps)); assert.equal(calls.length, 0);
  f.multi.relayers[2].hermes.ready = true;
  await assert.rejects(startFresh(f, { ...deps, relayAction: async () => ({ ok: false }) }));
  assert.equal(calls.length, 0);
});

test('a generation funded for three Relayers can bootstrap only R1 before filter verification', async t => {
  const f = fixture(t), file = path.join(f.output, 'prepared/manifest.json');
  const manifest = JSON.parse(fs.readFileSync(file));
  manifest.relayers = [1,2,3].map(i => ({ name: 'relayer-' + i, address: 'funded-fixture-' + i }));
  fs.writeFileSync(file, JSON.stringify(manifest));
  const calls = [];
  const result = await startFresh(f, { execute: async (program, args) => {
    if (program === process.execPath) calls.push(args[1]);
  }, relayAction: async () => { throw Error('R2/R3 must not start before filter verification'); } });
  assert.equal(result.relayers, 1);
  assert.deepEqual(calls, ['doctor', 'start', 'health', 'channels', 'health', 'relayer-start']);
});

test('a failed vulnerability scan blocks all SSH and all startup operations', async t => {
  const f = fixture(t), calls = [];
  await assert.rejects(startFresh(f, { execute: async (p,args) => { calls.push({ p,args }); throw Error('security scan not passed'); } }));
  assert.deepEqual(calls, [{ p: 'make', args: ['govulncheck'] }]);
});

test('binary scan failure or post-scan replacement blocks every SSH and start', async t => {
  const f = fixture(t), calls = [];
  await assert.rejects(startFresh(f, { execute: async (p, args) => {
    calls.push({ p, args }); if (args[0] === 'govulncheck-binary') throw Error('vulnerable deployment binary');
  } }));
  assert.ok(calls.every(c => c.p === 'make')); assert.equal(calls.length, 2);
  calls.length = 0;
  await assert.rejects(startFresh(f, { execute: async (p, args) => {
    calls.push({ p, args }); if (args[0] === 'govulncheck-binary') fs.writeFileSync(args[1].slice('BINARY='.length), 'changed-binary');
  } }));
  assert.ok(calls.every(c => c.p === 'make'));
});

test('realistic large binaries are hashed incrementally; symlinks and hardlinks are rejected', t => {
  const f = fixture(t), file = path.join(f.buildDir, 'large-binary');
  fs.closeSync(fs.openSync(file, 'wx')); fs.truncateSync(file, 65 * 2 ** 20);
  const expected = createHash('sha256');
  for (let i = 0; i < 65; i++) expected.update(Buffer.alloc(2 ** 20));
  assert.equal(binaryHash(file), expected.digest('hex'));
  fs.symlinkSync(file, file + '-alias'); assert.throws(() => binaryHash(file + '-alias'));
  fs.linkSync(file, file + '-link'); assert.throws(() => binaryHash(file));
});
