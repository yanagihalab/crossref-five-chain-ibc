import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { prepareLocalSecrets } from '../../docker/scripts/prepare-local-secrets.mjs';

test('local test identities are generated only at runtime, read back and never replaced', t => {
  const parent = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'crossref-local-secrets-test-')));
  t.after(() => fs.rmSync(parent, { recursive: true, force: true }));
  const destination = path.join(parent, 'new-identities');
  let calls = 0;
  const execute = (_binary, args, options) => {
    calls++;
    assert.ok(!args.join(' ').includes('synthetic'));
    assert.deepEqual(options.stdio, ['ignore', 'pipe', 'pipe']);
    return JSON.stringify({ mnemonic: Array(24).fill('synthetic').join(' ') });
  };
  const result = prepareLocalSecrets('/nonexistent/mock-cli', destination, execute);
  assert.equal(calls, 2);
  assert.equal(result.secretValuesPrinted, false);
  assert.equal(fs.statSync(path.join(destination, 'relayer.mnemonic')).mode & 0o777, 0o600);
  const before = fs.readFileSync(path.join(destination, 'validator.mnemonic'));
  assert.throws(() => prepareLocalSecrets('/nonexistent/mock-cli', destination, execute), /never overwrite/);
  assert.equal(calls, 2);
  assert.deepEqual(fs.readFileSync(path.join(destination, 'validator.mnemonic')), before);
});

test('CLI errors retain partial generation and suppress raw credential-bearing output', t => {
  const parent = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'crossref-local-secrets-failure-')));
  t.after(() => fs.rmSync(parent, { recursive: true, force: true }));
  assert.throws(() => prepareLocalSecrets('/nonexistent/mock-cli', path.join(parent, 'new-identities'), () => {
    throw Error('fixture-private-output');
  }), error => !error.message.includes('fixture-private-output') && error.message.includes('suppressed'));
  assert.equal(fs.readdirSync(parent).length, 1);
});
