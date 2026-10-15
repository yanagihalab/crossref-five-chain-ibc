#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
export const pin = {
  module: 'github.com/cosmos/cosmos-sdk', version: 'v0.54.3',
  sum: 'h1:oUxbIACmHZV13IZXl59y+mIEOCFlUAjXV9p1rqrYcEg=',
  modSum: 'h1:Unt21RO+q1EebU6gcUPKYVzpAQSr4KIHjaOmjpz2twA=',
  armorSHA256: 'f42b1209c9c40e8cde89652635e4762d44acb8b653d84e4bdf9244b9168ee0b9',
  maintainedArmorModule: 'github.com/ProtonMail/go-crypto', maintainedArmorVersion: 'v1.5.2',
};
const hash = b => createHash('sha256').update(b).digest('hex');
export function patchArmor(bytes) {
  assert.equal(hash(bytes), pin.armorSHA256, 'Unexpected upstream SDK armor source');
  const old = '"golang.org/x/crypto/openpgp/armor" //nolint:staticcheck //TODO: remove this dependency';
  const text = bytes.toString('utf8');
  assert.equal(text.split(old).length, 2);
  return Buffer.from(text.replace(old, '"github.com/ProtonMail/go-crypto/openpgp/armor"'));
}
export function inventory(folder) {
  const result = [];
  const walk = (dir, prefix = '') => {
    for (const name of fs.readdirSync(dir).sort()) {
      const file = path.join(dir, name), rel = prefix + name, st = fs.lstatSync(file);
      assert.ok(!st.isSymbolicLink(), 'SDK source symlink rejected');
      if (st.isDirectory()) walk(file, rel + '/');
      else { assert.ok(st.isFile() && st.nlink === 1); const bytes = fs.readFileSync(file); result.push({ path: rel, bytes: bytes.length, sha256: hash(bytes) }); }
    }
  };
  walk(folder); return result;
}
export function validateCopy(upstream, prepared, originalArmor, patchedArmor) {
  assert.equal(upstream.length, prepared.length);
  const originals = new Map(upstream.map(x => [x.path, x]));
  for (const item of prepared) {
    const expected = originals.get(item.path); assert.ok(expected);
    if (item.path === 'crypto/armor.go') {
      assert.equal(expected.sha256, hash(originalArmor)); assert.equal(item.sha256, hash(patchedArmor));
    } else assert.deepEqual(item, expected, 'Unexpected SDK source change: ' + item.path);
  }
}
const writeExclusive = (file, bytes) => {
  const fd = fs.openSync(file, 'wx', 0o600);
  try { fs.writeFileSync(fd, bytes); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
};
export function prepareSDK() {
  const cache = path.join(root, '.security-deps'), destination = path.join(cache, 'cosmos-sdk');
  const metadata = JSON.parse(execFileSync('go', ['mod', 'download', '-json', pin.module + '@' + pin.version], { cwd: root, maxBuffer: 1024 * 1024, encoding: 'utf8' }));
  assert.equal(metadata.Path, pin.module); assert.equal(metadata.Version, pin.version);
  assert.equal(metadata.Sum, pin.sum); assert.equal(metadata.GoModSum, pin.modSum); assert.ok(!metadata.Error);
  const original = fs.readFileSync(path.join(metadata.Dir, 'crypto/armor.go')), patched = patchArmor(original);
  fs.mkdirSync(cache, { recursive: true, mode: 0o700 });
  const verification = path.join(cache, 'upstream-verification');
  fs.mkdirSync(verification, { recursive: true, mode: 0o700 });
  const config = {
    'go.mod': `module github.com/crossref/sdk-upstream-verification\n\ngo 1.26.9\n\nrequire ${pin.module} ${pin.version}\n`,
    'go.sum': `${pin.module} ${pin.version} ${pin.sum}\n${pin.module} ${pin.version}/go.mod ${pin.modSum}\n`,
  };
  for (const [name, contents] of Object.entries(config)) {
    const file = path.join(verification, name);
    if (!fs.existsSync(file)) writeExclusive(file, contents);
    assert.ok(fs.lstatSync(file).isFile() && !fs.lstatSync(file).isSymbolicLink());
    assert.equal(fs.readFileSync(file, 'utf8'), contents);
  }
  execFileSync('go', ['mod', 'verify'], { cwd: verification, timeout: 180000, maxBuffer: 1024 * 1024 });
  const upstream = inventory(metadata.Dir);
  if (fs.existsSync(destination)) {
    assert.ok(fs.lstatSync(destination).isDirectory() && !fs.lstatSync(destination).isSymbolicLink());
    const receipt = JSON.parse(fs.readFileSync(path.join(cache, 'sdk-source-receipt.json')));
    assert.deepEqual(receipt.pin, pin); assert.deepEqual(receipt.upstreamFiles, upstream);
    validateCopy(upstream, inventory(destination), original, patched);
    return receipt;
  }
  const lock = path.join(cache, 'prepare.lock');
  const fd = fs.openSync(lock, 'wx', 0o600); fs.closeSync(fd);
  const staging = path.join(cache, 'attempt-' + randomUUID());
  try {
    fs.cpSync(metadata.Dir, staging, { recursive: true, errorOnExist: true, force: false });
    fs.chmodSync(path.join(staging, 'crypto/armor.go'), 0o600);
    fs.writeFileSync(path.join(staging, 'crypto/armor.go'), patched);
    validateCopy(upstream, inventory(staging), original, patched);
    const receipt = { pin, upstreamFiles: upstream, preparedFiles: inventory(staging), changedFile: 'crypto/armor.go', originalModuleCacheUnchanged: true };
    fs.renameSync(staging, destination);
    writeExclusive(path.join(cache, 'sdk-source-receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
    assert.equal(hash(fs.readFileSync(path.join(metadata.Dir, 'crypto/armor.go'))), pin.armorSHA256);
    return receipt;
  } finally { fs.unlinkSync(lock); }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { const receipt = prepareSDK(); console.log(JSON.stringify({ sdkPrepared: true, version: pin.version, upstreamFiles: receipt.upstreamFiles.length, changedFile: receipt.changedFile })); }
  catch (error) { console.error('Verified SDK preparation failed: ' + error.message); process.exitCode = 1; }
}
