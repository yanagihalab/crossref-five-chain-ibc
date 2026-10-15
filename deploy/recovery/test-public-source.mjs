import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { inspectPublicFile } from './check-public-source.mjs';
import { adminProxyOptions } from '../network/lib.mjs';
import { validateConfig, executeAction } from '../network/manage-relayers.mjs';

test('public distribution rejects runtime identities, credentials and private payloads', () => {
  for (const name of ['.env', 'secrets/password', 'operator.pem', 'relayer.mnemonic',
    'config/priv_validator_key.json', 'config/node_key.json', 'data/priv_validator_state.json',
    'keyring-test/key', 'deploy/network/generated/state.json', '.security-deps/secret']) {
    assert.ok(inspectPublicFile(name, '').includes('private-runtime-path'));
  }
  for (const text of ['-----BEGIN ' + 'OPENSSH PRIVATE KEY-----',
    JSON.stringify({ priv_key: { value: 'a'.repeat(88) } }),
    '/Users/' + 'operator/private', '/Volumes/' + 'backup/private', 'vps' + '123456']) {
    assert.ok(inspectPublicFile('fixture.txt', text).length);
  }
  assert.deepEqual(inspectPublicFile('inventory.example.json', '{"address":"192.0.2.1"}'), []);
});

test('public source retains all 32 explicit remediation records', () => {
  const record = JSON.parse(fs.readFileSync(new URL('./security-remediation-20261014.json', import.meta.url)));
  assert.equal(record.entries.length, 32);
  assert.equal(new Set(record.entries.map(row => row.id)).size, 32);
  assert.ok(record.entries.every(row => row.symbolFindingAbsentInFinalScan));
  assert.equal(record.sourceSymbolFindings, 0);
  assert.equal(record.linuxBinarySymbolFindings, 0);
  assert.ok(record.unusedModuleOnlyFindings.length > 0);
});

test('public default relayer configuration cannot connect or start', async () => {
  const raw = JSON.parse(fs.readFileSync(new URL('../network/multi-relayer.example.json', import.meta.url)));
  const config = validateConfig(raw);
  let calls = 0;
  const result = await executeAction(config, 'start', config.relayers, {
    verifySSHFiles: false, run: async () => { calls++; throw Error('must not connect'); },
  });
  assert.equal(result.ok, false);
  assert.equal(calls, 0);
  assert.ok(config.relayers.every(row => !row.hostConfigurationVerified && !row.hermes.ready));
});

test('public distribution never reuses the historical administrative proxy', () => {
  const before = process.env.CROSSREF_ADMIN_PROXY;
  try {
    process.env.CROSSREF_ADMIN_PROXY = '192.0.2.1';
    assert.throws(() => adminProxyOptions({ address: '192.0.2.1' }));
    assert.throws(() => adminProxyOptions({ address: '192.0.2.2' }));
    delete process.env.CROSSREF_ADMIN_PROXY;
    assert.deepEqual(adminProxyOptions({ address: '192.0.2.2' }), []);
  } finally {
    if (before === undefined) delete process.env.CROSSREF_ADMIN_PROXY;
    else process.env.CROSSREF_ADMIN_PROXY = before;
  }
});
