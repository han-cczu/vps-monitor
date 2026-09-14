import test from 'node:test';
import assert from 'node:assert/strict';
import { parseThresholds } from '../src/sections/alerts/shared.ts';

test('alert threshold form accepts selected source thresholds and rejects typos or duplicates', () => {
  assert.deepEqual(parseThresholds('80, 90，100', [80, 90, 100]), [80, 90, 100]);
  assert.deepEqual(parseThresholds('7\n1', [7, 3, 1]), [7, 1]);
  for (const value of ['', '80,80', '81', 'NaN', '100.1']) {
    assert.throws(() => parseThresholds(value, [80, 90, 100]));
  }
  assert.throws(() => parseThresholds('90', [80, 100]));
});
