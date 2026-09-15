import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';
import test from 'node:test';

// Resolve the same src alias that Vite uses, then execute the actual form/display helpers.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith('src/')) {
      return nextResolve(new URL(`../${specifier}.ts`, import.meta.url).href, context);
    }
    return nextResolve(specifier, context);
  },
});
const { joinTraffic, splitTraffic, formatTrafficLimit } =
  await import('../src/sections/servers/utils.ts');
const { formatBytes, formatTrafficBytes, formatRate, setFormatPreferences } =
  await import('../src/utils/format.ts');
hooks.deregister();

test('a 500 GB plan stays 500 GB through form, list, card and remaining usage', () => {
  const limit = joinTraffic(500, 'GB');
  assert.equal(limit, 500_000_000_000);
  assert.deepEqual(splitTraffic(limit), { value: 500, unit: 'GB' });
  assert.equal(formatTrafficLimit(limit), '500 GB');
  assert.equal(formatTrafficBytes(limit), '500 GB');
  assert.equal(formatTrafficBytes(limit - 10_000_000_000), '490 GB');
});

test('GB and TB are decimal and unlimited plans remain unlimited', () => {
  assert.equal(joinTraffic(1, 'TB'), joinTraffic(1000, 'GB'));
  assert.deepEqual(splitTraffic(1_000_000_000_000), { value: 1, unit: 'TB' });
  assert.equal(formatTrafficLimit(joinTraffic(1.5, 'TB')), '1.5 TB');
  assert.equal(joinTraffic(0, 'GB'), 0);
  assert.deepEqual(splitTraffic(0), { value: 0, unit: 'GB' });
  assert.equal(formatTrafficLimit(0), '不限');
});

test('editing an existing binary-era quota preserves its bytes until explicitly changed', () => {
  for (const bytes of [536_870_912_000, 1_099_511_627_776, 500_000_000_001]) {
    const form = splitTraffic(bytes);
    assert.equal(joinTraffic(form.value, form.unit), bytes);
  }
  assert.deepEqual(splitTraffic(536_870_912_000), { value: 536.870912, unit: 'GB' });
  assert.equal(formatTrafficLimit(536_870_912_000), '536.87 GB');
});

test('binary storage preferences cannot change traffic quotas, usage or network rates', () => {
  setFormatPreferences(1024, 'UTC');
  try {
    assert.equal(formatBytes(1_073_741_824), '1 GiB');
    assert.equal(formatTrafficBytes(500_000_000_000), '500 GB');
    assert.equal(formatTrafficLimit(500_000_000_000), '500 GB');
    assert.equal(formatTrafficBytes(10_000_000_000), '10 GB');
    assert.equal(formatRate(1_000_000), '1 MB/s');
  } finally {
    setFormatPreferences(1000, 'Asia/Shanghai');
  }
});
