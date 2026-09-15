import assert from 'node:assert/strict';
import test from 'node:test';
import { canEditManagedProxy, observedBytes } from '../src/sections/proxy/observation-helpers.ts';

test('unknown, external and offline instances never expose managed editing', () => {
  assert.equal(canEditManagedProxy(), false);
  for (const management of ['unknown', 'external']) assert.equal(canEditManagedProxy({ online: true, management }), false);
  assert.equal(canEditManagedProxy({ online: false, management: 'managed' }), false);
  for (const management of ['managed', 'none']) assert.equal(canEditManagedProxy({ online: true, management }), true);
});
test('missing external counters stay unavailable instead of zero', () => {
  for (const n of [null, undefined, -1, NaN]) assert.equal(observedBytes(n), '不可用');
  assert.equal(observedBytes(0), '0 B');
  assert.equal(observedBytes(1024 ** 3), '1 GiB');
});
