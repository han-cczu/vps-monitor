import assert from 'node:assert/strict';
import test from 'node:test';

import { nextTrafficReset } from '../src/utils/traffic-schedule.ts';

test('30-day cadence is distinct from monthly dates, including leap years and year boundaries', () => {
  assert.equal(nextTrafficReset('2026-08-15', 'days', 15), '2026-09-14');
  assert.equal(nextTrafficReset('2026-08-15', 'monthly', 15), '2026-09-15');
  assert.equal(nextTrafficReset('2026-01-31', 'days', 31), '2026-03-02');
  assert.equal(nextTrafficReset('2026-01-31', 'monthly', 31), '2026-02-28');
  assert.equal(nextTrafficReset('2028-01-31', 'monthly', 31), '2028-02-29');
  assert.equal(nextTrafficReset('2026-02-28', 'monthly', 31), '2026-03-31');
  assert.equal(nextTrafficReset('2026-12-15', 'days', 15), '2027-01-14');
  assert.equal(nextTrafficReset('2026-03-01', 'days', 1), '2026-03-31');
});

test('empty or invalid dates do not produce a misleading reset date', () => {
  for (const value of ['', 'invalid', '2026-02-30', '2026-13-01']) {
    assert.equal(nextTrafficReset(value, 'days', 1), '');
  }
  for (const day of [0, 32, 1.5, NaN]) assert.equal(nextTrafficReset('2026-09-15', 'monthly', day), '');
});
