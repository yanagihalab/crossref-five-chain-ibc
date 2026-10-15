import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertSDKPackages } from './check-sdk-advisories.mjs';
import { pin } from './prepare-sdk.mjs';

const advisory = id => ({ id, affected: [{ package: { name: pin.module }, ecosystem_specific: { imports: [{ path: pin.module + '/x/crisis' }] } }] });
test('explicit upstream SDK query cannot hide an imported crisis package', () => {
  const known = [advisory('GO-2023-1821'), advisory('GO-2023-1881')];
  assertSDKPackages(known, new Set(['github.com/cosmos/cosmos-sdk/baseapp']));
  assert.throws(() => assertSDKPackages(known, new Set([pin.module + '/x/crisis'])));
  assert.throws(() => assertSDKPackages([advisory('GO-2099-0001')], new Set()));
  const changed = advisory('GO-2023-1821'); changed.affected[0].ecosystem_specific.imports[0].path = pin.module + '/other';
  assert.throws(() => assertSDKPackages([changed], new Set()));
});
