import assert from 'node:assert/strict';
import test from 'node:test';

import { calibrationBytes, calibrationGB, calibratedUsage } from '../src/utils/traffic-calibration.ts';

test('calibration uses decimal units and exact byte conversion', () => {
  assert.equal(calibrationBytes('300', 'GB'), 300_000_000_000);
  assert.equal(calibrationBytes('1', 'TB'), 1_000_000_000_000);
  assert.equal(calibrationBytes('1.000000001', 'GB'), 1_000_000_001);
  assert.equal(calibrationBytes('0.5', 'B'), 1);
  assert.equal(calibrationBytes('0', 'GB'), 0);
  for (const bytes of [0, 1, 3_123_456_789, Number.MAX_SAFE_INTEGER]) {
    assert.equal(calibrationBytes(calibrationGB(bytes), 'GB'), bytes);
  }
});

test('calibration rejects missing, negative, malformed and imprecise inputs', () => {
  for (const value of ['', ' ', '-1', 'NaN', 'Infinity', '1e3', '1,000', '.5', '0.1234567890123', '9007199254740992']) {
    assert.equal(calibrationBytes(value, 'B'), null, value);
  }
  assert.equal(calibrationBytes('9007199254740991', 'B'), Number.MAX_SAFE_INTEGER);
});

test('separate calibrated directions feed the selected billing mode', () => {
  const incoming = calibrationBytes('100', 'GB');
  const outgoing = calibrationBytes('200', 'GB');
  assert.equal(calibratedUsage(incoming, outgoing, 'sum'), 300_000_000_000);
  assert.equal(calibratedUsage(incoming, outgoing, 'in'), incoming);
  assert.equal(calibratedUsage(incoming, outgoing, 'out'), outgoing);
  assert.equal(calibratedUsage(incoming, outgoing, 'max'), outgoing);
});
