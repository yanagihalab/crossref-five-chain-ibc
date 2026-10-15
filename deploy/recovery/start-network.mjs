#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { loadInventory, resolvePath, hostFor, hostRoot, remoteCompose, sshArgs, quote, render } from '../network/lib.mjs';
import { run, prepare, verifyPrepared } from '../network/orchestrator.mjs';
import { validateConfig, executeAction } from '../network/manage-relayers.mjs';

const SELF = fileURLToPath(import.meta.url);
const ORCHESTRATOR = fileURLToPath(new URL('../network/orchestrator.mjs', import.meta.url));
export const retiredAddressHashes = new Set([
  "5d1bb1b55ba9f7c8b4b048e4c7cc7f5193e56dcb178f6fe2d95f83a912b47e18",
  "bbe7e8b947e5c11393b09639832e71ec0a5c1a49256feefb7ae0947be46d1485",
  "30aeaf8414966ec1d3689a0c41d27be204d85706247cec7550e116d87615729d",
  "e8a0e5e9239fe6614297a5a76e96cc02aa87953fed914b12a047d2bcdd5b3f96",
  "408c782220dff3835a8397b68c805d045c89e840a1d3b522b504215d2e7d04a9",
  "f298650c9a166929fb40cc8a1b079e55a05b4f8ccbb0ffb864ba4c6d092d1c46",
  "1f35b6e67459f6043108f7c3617ad0d40eab7cbf86ea3d4fb2f9bf2ac72c17c9",
  "ea09f7cc66e8073324143e66467e747f7456b263b31130c695f67497e22b6cf7",
  "59eca2fabc3ea8b439727b93b9c7f77e6431c74a96a2e78fe01812b951e585e4",
  "53810c4a9339b5615d21c393a227b1b906c6d3e7228755e4fc8b5bc116cd6de3",
  "c8afe3431637cc25b77f6dddadda64549900592b63a5d2722a897deed4e5f72e",
  "6afb4b1177c1c5afd98b79a48477227b50391b5108fe6bca922846ccb0f30195",
  "e1eaa3dbb0cd3f7c679e957608924834755ef39584605a9011e57f815fbe0db8",
  "72984de1384922134194f71440aa21fac696e20dfa703f8538224b9aa163aa02",
  "2f82dcccc196980b4c26ff3d6d5b44e5c99c74bbcf9125340dfc5efe1ed78f4f"
]);
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const homeFiles = ['config/genesis.json','config/priv_validator_key.json','config/node_key.json',
  'config/config.toml','config/app.toml','data/priv_validator_state.json'];

function regular(file) {
  const st = fs.lstatSync(file);
  assert.ok(st.isFile() && !st.isSymbolicLink() && st.nlink === 1 && st.size > 0 && st.size < 64 * 2 ** 20, 'Unsafe or missing recovery file');
  return fs.readFileSync(file);
}

export function binaryHash(file) {
  const before = fs.lstatSync(file);
  assert.ok(before.isFile() && !before.isSymbolicLink() && before.nlink === 1 && before.size > 0 && before.size < 1024 * 2 ** 20, 'Unsafe or missing deployment binary');
  const fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW), hash = createHash('sha256');
  try {
    const st = fs.fstatSync(fd); assert.equal(st.ino, before.ino); assert.equal(st.dev, before.dev);
    const buffer = Buffer.alloc(256 * 1024);
    let count;
    while ((count = fs.readSync(fd, buffer, 0, buffer.length, null)) > 0) hash.update(buffer.subarray(0, count));
    const after = fs.fstatSync(fd);
    for (const key of ['ino', 'dev', 'size', 'mtimeMs', 'ctimeMs']) assert.equal(after[key], before[key], 'Deployment binary changed during read');
  } finally { fs.closeSync(fd); }
  return hash.digest('hex');
}

export function auditHome(home, expected) {
  const root = fs.realpathSync(home);
  const hashes = {};
  for (const name of homeFiles) {
    const file = path.join(root, name);
    assert.equal(fs.realpathSync(file), file, 'Recovery home must not contain symlink aliases');
    hashes[name] = digest(regular(file));
    assert.equal(hashes[name], expected.fileSHA256?.[name], 'Restored file differs from retained checkpoint: ' + name);
  }
  const genesis = JSON.parse(regular(path.join(root, 'config/genesis.json')));
  const state = JSON.parse(regular(path.join(root, 'data/priv_validator_state.json')));
  assert.equal(genesis.chain_id, expected.chainId);
  assert.match(String(state.height), /^\d+$/);
  assert.equal(String(state.height), String(expected.signingHeight));
  return { chainId: genesis.chain_id, signingHeight: String(state.height), fileSHA256: hashes,
    readOnly: true, nodeStarted: false, databaseVerificationReplaced: false };
}

export function validateTargets(inv, multi) {
  assert.ok(!process.env.CROSSREF_ADMIN_PROXY, 'Clear the historical administrative proxy before new-host startup');
  assert.equal(inv.chains.length, 3, 'This launcher requires three chains');
  assert.equal(inv.nodes.length, 12);
  assert.ok(inv.chains.every(c => c.validators.length === 4));
  assert.ok(inv.relayer, 'A dedicated Relayer is required');
  for (const host of inv.hosts) for (const address of [host.address, host.endpoint, host.bindAddress]) {
    assert.ok(!retiredAddressHashes.has(digest(String(address))), 'Historical retired VPS address rejected');
  }
  assert.ok(inv.network.startsWith('fresh-'), 'Fresh startup requires a new fresh-* network name');
  if (!multi) return;
  assert.equal(multi.relayers.length, 3);
  assert.deepEqual(multi.relayers.map(r => r.name).sort(), ['crossref-relayer-1','crossref-relayer-2','crossref-relayer-3']);
  assert.equal(new Set(multi.relayers.map(r => r.address)).size, 3);
  const relay = hostFor(inv, inv.relayer.host);
  assert.equal(multi.relayers.find(r => r.name === 'crossref-relayer-1')?.address, relay.address);
  const validatorAddresses = new Set(inv.hosts.filter(h => h.id !== relay.id).map(h => h.address));
  for (const r of multi.relayers) {
    assert.ok(!retiredAddressHashes.has(digest(r.address)) && !validatorAddresses.has(r.address), 'Relayer target overlaps a Validator or retired VPS');
  }
}

export function freshPreflight(inv, node, output, binarySHA256) {
  const host = hostFor(inv, node.hostId), root = hostRoot(inv);
  const home = path.join(output, 'prepared/homes', node.id);
  const checks = homeFiles.map(name => {
    const expected = digest(regular(path.join(home, name)));
    return `test "$(sha256sum ${quote(root + '/homes/' + node.id + '/' + name)} | cut -d ' ' -f 1)" = ${quote(expected)}`;
  });
  return `set -eu
test "$(timedatectl show -p NTPSynchronized --value)" = yes
test -z "$(docker ps -q)"
command -v pgrep >/dev/null
if pgrep -x crossrefd >/dev/null || pgrep -x crossrefdd >/dev/null; then exit 1; fi
test ! -e ${quote(root + '/run-once.json')}
test ! -e ${quote(root + '/results/run-once.json')}
test ! -e ${quote(root + '/homes/' + node.id + '/data/application.db')}
test ! -e ${quote(root + '/homes/' + node.id + '/data/blockstore.db')}
${checks.join('\n')}
${binarySHA256 ? `test "$(sha256sum ${quote(root + '/crossrefd')} | cut -d ' ' -f 1)" = ${quote(binarySHA256)}` : ''}
${remoteCompose(inv, host, ['config', '--quiet'])}`;
}

export async function startFresh(context, { execute = run, relayAction = executeAction } = {}) {
  const { inv, output, inventory, confirmation, multi } = context;
  const progress = context.progress ?? (() => {});
  validateTargets(inv, multi);
  assert.equal(confirmation, inv.network, 'Explicit --confirm-new-network is required');
  const manifest = verifyPrepared(inv, output);
  await execute('make', ['govulncheck'], { quiet: true });
  progress({ stage: 'vulnerability-check-passed' });
  const binaries = new Map();
  for (const architecture of new Set(inv.nodes.map(node => hostFor(inv, node.hostId).architecture))) {
    const file = path.resolve(context.buildDir ?? 'build', 'crossrefd-linux-' + architecture);
    const sha256 = binaryHash(file);
    await execute('make', ['govulncheck-binary', 'BINARY=' + file], { quiet: true });
    assert.equal(binaryHash(file), sha256, 'Scanned binary changed');
    binaries.set(architecture, { file, sha256 });
  }
  progress({ stage: 'deployment-binaries-vulnerability-checked' });
  if (multi) {
    assert.equal(manifest.relayers?.length, 3, 'Prepare and fund three independent Relayer identities first');
    assert.ok(multi.relayers.every(r => r.hostConfigurationVerified && r.hermes.ready), 'All Relayer deployments must be independently verified');
    assert.equal((await relayAction(multi, 'check', multi.relayers)).ok, true, 'Relayer preflight failed');
    progress({ stage: 'relayers-preflighted' });
  }
  // Every new Validator must match an unused prepared identity before any starts.
  for (const node of inv.nodes) {
    const host = hostFor(inv, node.hostId);
    const binary = binaries.get(host.architecture);
    assert.equal(binaryHash(binary.file), binary.sha256, 'Scanned binary changed before deployment check');
    await execute('ssh', ['-F', '/dev/null', '-o', 'IdentitiesOnly=yes', ...sshArgs(host), freshPreflight(inv, node, output, binary.sha256)], { quiet: true });
    progress({ stage: 'validator-preflighted', node: node.id });
  }
  if (!multi) {
    const relay = hostFor(inv, inv.relayer.host);
    const mnemonicHash = digest(regular(path.join(output, 'prepared/relayer.mnemonic')));
    await execute('ssh', ['-F', '/dev/null', '-o', 'IdentitiesOnly=yes', ...sshArgs(relay), `set -eu
test -z "$(docker ps -q)"
command -v pgrep >/dev/null
if pgrep -x hermes >/dev/null; then exit 1; fi
test "$(sha256sum ${quote(hostRoot(inv) + '/secrets/relayer.mnemonic')} | cut -d ' ' -f 1)" = ${quote(mnemonicHash)}
${remoteCompose(inv, relay, ['config', '--quiet'])}`], { quiet: true });
    progress({ stage: 'relayer-preflighted' });
  }
  const orchestrate = async command => {
    for (const binary of binaries.values()) assert.equal(binaryHash(binary.file), binary.sha256);
    await execute(process.execPath, [ORCHESTRATOR, command, '--inventory', inventory, '--output-dir', output], { quiet: true });
    progress({ stage: command + '-completed' });
  };
  await orchestrate('doctor');
  await orchestrate('start');
  await orchestrate('health');
  await orchestrate('channels');
  await orchestrate('health');
  if (multi) assert.equal((await relayAction(multi, 'start', multi.relayers)).ok, true, 'Relayer start incomplete; inspect existing processes without replaying transactions');
  else await orchestrate('relayer-start');
  return { network: inv.network, validators: 12, relayers: multi ? 3 : 1, startupCompleted: true,
    providerOperationsPerformed: false, oldTransactionsReplayed: false, originalDataDeleted: false };
}

export async function main(argv = process.argv.slice(2)) {
  const [command, ...args] = argv;
  if (!command || command === 'help') {
    console.log('plan|prepare-fresh|start-fresh --inventory FILE --output-dir DIR [--multi-relayer-config FILE] [--binary FILE] [--confirm-new-network NAME]\naudit-home --home DIR --expected-state FILE\nNo restore initialization, provider operations, or automatic retries. See docs/NODE-STARTUP.ja.md.');
    return;
  }
  const { values } = parseArgs({ args, options: {
    inventory: { type: 'string' }, 'output-dir': { type: 'string' }, 'multi-relayer-config': { type: 'string' },
    'confirm-new-network': { type: 'string' }, binary: { type: 'string', default: 'build/crossrefdd' }, 'build-dir': { type: 'string', default: 'build' },
    home: { type: 'string' }, 'expected-state': { type: 'string' },
  } });
  if (command === 'audit-home') {
    assert.ok(values.home && values['expected-state']);
    console.log(JSON.stringify(auditHome(path.resolve(values.home), JSON.parse(regular(path.resolve(values['expected-state'])))), null, 2));
    return;
  }
  assert.ok(['plan','prepare-fresh','start-fresh'].includes(command));
  assert.ok(values.inventory && values['output-dir'], 'Explicit inventory and output directory are required');
  const inventory = resolvePath(values.inventory), output = resolvePath(values['output-dir']);
  const inv = loadInventory(inventory);
  const multi = values['multi-relayer-config'] ? validateConfig(JSON.parse(regular(resolvePath(values['multi-relayer-config'])))) : null;
  validateTargets(inv, multi);
  if (command === 'plan') {
    console.log(JSON.stringify({ network: inv.network, validators: 12, relayers: multi ? 3 : 1, remoteCallsPerformed: false,
      steps: ['new-host-and-private-network-setup','build','prepare-fresh','deploy-dry-run','deploy','all-host-readonly-preflight','validators-start','block-health','verified-channels','relayers-start'],
      retainedStateRestart: 'Separate offline audit and operator-reviewed migration; never use prepare-fresh on restored DBs.' }, null, 2));
  } else if (command === 'prepare-fresh') {
    assert.equal(values['confirm-new-network'], inv.network);
    assert.equal(fs.existsSync(path.join(output, 'prepared')), false, 'Never replace or reuse an existing prepared generation');
    await prepare(inv, output, resolvePath(values.binary), null, { relayerCount: multi ? 3 : 1 });
    render(inv, output);
  } else console.log(JSON.stringify(await startFresh({ inv, output, inventory, multi, buildDir: resolvePath(values['build-dir']), confirmation: values['confirm-new-network'],
    progress: event => console.error(JSON.stringify({ at: new Date().toISOString(), network: inv.network, ...event })) }), null, 2));
}

if (process.argv[1] && path.resolve(process.argv[1]) === SELF) {
  main().catch(() => { console.error('Startup blocked or incomplete. Raw SSH/configuration output suppressed; retain data and inspect the last completed stage.'); process.exitCode = 1; });
}
