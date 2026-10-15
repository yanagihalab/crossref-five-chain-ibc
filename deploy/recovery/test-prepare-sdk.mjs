import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { inventory, validateCopy, patchArmor } from './prepare-sdk.mjs';

const hash = b => createHash('sha256').update(b).digest('hex');
test('SDK patch refuses unrecognized upstream source', () => assert.throws(() => patchArmor(Buffer.from('modified source'))));
test('SDK source verification permits only the exact armor replacement', () => {
  const a = Buffer.from('original'), b = Buffer.from('maintained'), c = Buffer.from('unchanged');
  const entry = (p, bytes) => ({ path: p, bytes: bytes.length, sha256: hash(bytes) });
  const original = [entry('crypto/armor.go', a), entry('other.go', c)];
  const prepared = [entry('crypto/armor.go', b), entry('other.go', c)];
  validateCopy(original, prepared, a, b);
  assert.throws(() => validateCopy(original, [prepared[0]], a, b));
  assert.throws(() => validateCopy(original, [prepared[0], entry('other.go', b)], a, b));
});
test('SDK inventory rejects symlinks and hardlinks', t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sdk-source-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  fs.writeFileSync(path.join(dir, 'source'), 'fixture'); fs.symlinkSync('source', path.join(dir, 'alias'));
  assert.throws(() => inventory(dir)); fs.unlinkSync(path.join(dir, 'alias'));
  fs.linkSync(path.join(dir, 'source'), path.join(dir, 'linked')); assert.throws(() => inventory(dir));
});
