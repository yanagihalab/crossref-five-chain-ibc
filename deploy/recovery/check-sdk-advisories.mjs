#!/usr/bin/env node
import assert from 'node:assert/strict';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { pin, prepareSDK } from './prepare-sdk.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
export function assertSDKPackages(advisories, importedPackages) {
  const allowed = new Set(['GO-2023-1821', 'GO-2023-1881']);
  for (const advisory of advisories) {
    assert.ok(allowed.has(advisory.id), 'New upstream SDK advisory requires review: ' + advisory.id);
    const affected = advisory.affected.filter(a => a.package.name === pin.module);
    assert.ok(affected.length > 0);
    for (const entry of affected) {
      assert.ok(entry.ecosystem_specific?.imports?.length > 0);
      for (const item of entry.ecosystem_specific.imports) {
        assert.equal(item.path, pin.module + '/x/crisis');
        assert.ok(!importedPackages.has(item.path), 'Affected SDK package is now imported: ' + item.path);
      }
    }
  }
}
export function checkSDKAdvisories() {
  prepareSDK();
  const run = args => execFileSync('go', args, { cwd: root, maxBuffer: 64 * 1024 * 1024, timeout: 300000 });
  // Local source replacements lose upstream version metadata; query that exact version separately.
  const stream = run(['run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', '-json', '-mode=query', pin.module + '@' + pin.version]);
  const decode = input => JSON.parse(execFileSync('go', ['run', './deploy/recovery/json-stream-decode'], { cwd: root, input, maxBuffer: 64 * 1024 * 1024, timeout: 300000 }));
  const advisories = decode(stream).filter(m => m.osv).map(m => m.osv);
  const packages = decode(run(['list', '-mod=readonly', '-deps', '-json', './...']));
  const imports = new Set(packages.map(p => p.ImportPath));
  assertSDKPackages(advisories, imports);
  const forbidden = [...imports].filter(p => p === 'github.com/pion/dtls/v2' || p.startsWith('github.com/pion/dtls/v2/') || p === 'golang.org/x/crypto/openpgp' || p.startsWith('golang.org/x/crypto/openpgp/'));
  assert.deepEqual(forbidden, [], 'Retired cryptographic packages remain in the runtime graph');
  return { upstreamVersion: pin.version, upstreamAdvisories: advisories.map(a => a.id), affectedPackagesAbsent: true, oldOpenPGPAndDTLSv2Absent: true };
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(checkSDKAdvisories())); }
  catch (error) { console.error('SDK advisory gate failed: ' + error.message); process.exitCode = 1; }
}
