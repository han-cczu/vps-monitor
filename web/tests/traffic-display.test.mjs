import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { useRealtime } from '../src/store/realtime.ts';
import { selectTrafficTotals } from '../src/utils/traffic-display.ts';

const fixture = JSON.parse(
  readFileSync(new URL('../../server/internal/hub/testdata/snapshot.json', import.meta.url), 'utf8')
);

test('billing and system counters stay distinct, including unknown and reset-to-zero values', () => {
  const server = structuredClone(fixture.servers[0]);
  server.traffic = { in: 30, out: 70 };
  server.net.boot_in_total = 1000;
  server.net.boot_out_total = 2000;
  assert.deepEqual(selectTrafficTotals(server, 'period'), { in: 30, out: 70 });
  assert.deepEqual(selectTrafficTotals(server, 'boot'), { in: 1000, out: 2000 });
  server.net.boot_in_total = 0;
  server.net.boot_out_total = 0;
  assert.deepEqual(selectTrafficTotals(server, 'boot'), { in: 0, out: 0 });
  assert.deepEqual(selectTrafficTotals(server, 'period'), { in: 30, out: 70 });
  server.net.boot_in_total = null;
  delete server.net.boot_out_total;
  assert.deepEqual(selectTrafficTotals(server, 'boot'), { in: null, out: null });
  server.traffic = null;
  assert.deepEqual(selectTrafficTotals(server, 'period'), { in: null, out: null });
});

test('a system-counter-only update invalidates the realtime card while unchanged snapshots retain identity', () => {
  useRealtime.getState().reset();
  const snapshot = structuredClone(fixture);
  useRealtime.getState().applySnapshot(structuredClone(snapshot));
  const initial = useRealtime.getState().servers[1];
  useRealtime.getState().applySnapshot(structuredClone(snapshot));
  assert.equal(useRealtime.getState().servers[1], initial);
  for (const key of ['boot_in_total', 'boot_out_total']) {
    const previous = useRealtime.getState().servers[1];
    snapshot.servers[0].net[key] = 42;
    useRealtime.getState().applySnapshot(structuredClone(snapshot));
    assert.notEqual(useRealtime.getState().servers[1], previous);
    assert.equal(useRealtime.getState().servers[1].net[key], 42);
  }
  useRealtime.getState().reset();
});

test('display choice restores from storage and still changes when browser storage is blocked', async () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const saved = new Map([['vps-monitor-traffic-scope', 'boot']]);
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: (key) => saved.get(key), setItem: (key, value) => saved.set(key, value) },
  });
  try {
    const { useTrafficDisplay } = await import('../src/store/traffic-display.ts');
    assert.equal(useTrafficDisplay.getState().scope, 'boot');
    useTrafficDisplay.getState().setScope('period');
    assert.equal(saved.get('vps-monitor-traffic-scope'), 'period');
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('blocked');
      },
    });
    assert.doesNotThrow(() => useTrafficDisplay.getState().setScope('boot'));
    assert.equal(useTrafficDisplay.getState().scope, 'boot');
  } finally {
    if (original) Object.defineProperty(globalThis, 'localStorage', original);
    else delete globalThis.localStorage;
  }
});
